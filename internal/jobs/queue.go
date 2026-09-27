// Package jobs is a small SQLite-backed job queue for a single process.
//
// Jobs survive restarts. A job interrupted by a restart is re-queued if it is
// safe to repeat, or handed to OnInterrupted for a human to decide if it has
// side effects (pushing, opening pull requests, merging).
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
)

// Job statuses.
const (
	StatusQueued      = "queued"
	StatusRunning     = "running"
	StatusDone        = "done"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
	StatusInterrupted = "interrupted"
)

// ErrDuplicate means an active job with the same dedupe key exists.
var ErrDuplicate = errors.New("an equivalent job is already queued or running")

// Handler does the work of one job.
type Handler func(ctx context.Context, job db.Job) error

// Kind configures how a job kind is run.
type Kind struct {
	Handler Handler
	// SideEffects marks jobs that are unsafe to repeat blindly.
	SideEffects bool
	// Claude marks jobs that spend the Claude quota; they are held back while
	// the account is rate limited.
	Claude bool
}

// RetryError asks for the job to be re-queued after a delay.
type RetryError struct {
	After time.Duration
	Err   error
}

func (e *RetryError) Error() string { return fmt.Sprintf("retry in %s: %v", e.After, e.Err) }
func (e *RetryError) Unwrap() error { return e.Err }

// Queue runs registered job kinds on a fixed number of workers.
type Queue struct {
	st      *store.Store
	workers int
	log     *slog.Logger

	kinds map[string]Kind

	// OnInterrupted is told about side-effect jobs found running at boot.
	OnInterrupted func(ctx context.Context, job db.Job)
	// OnChange is told whenever a job changes status.
	OnChange func(job db.Job)

	wake chan struct{}

	mu          sync.Mutex
	claimMu     sync.Mutex
	pausedUntil time.Time
	running     map[string]context.CancelFunc // job id → cancel
	unitJob     map[string]string             // unit id → running job id
	cancelled   map[string]bool               // job ids cancelled on purpose
}

// New creates a queue.
func New(st *store.Store, workers int, log *slog.Logger) *Queue {
	if workers < 1 {
		workers = 1
	}
	return &Queue{
		st:        st,
		workers:   workers,
		log:       log,
		kinds:     map[string]Kind{},
		wake:      make(chan struct{}, 1),
		running:   map[string]context.CancelFunc{},
		unitJob:   map[string]string{},
		cancelled: map[string]bool{},
	}
}

// Register adds a job kind. Call before Run.
func (q *Queue) Register(name string, k Kind) {
	q.kinds[name] = k
}

// EnqueueOpts describes a job to add.
type EnqueueOpts struct {
	Kind      string
	UnitID    string
	ProjectID string
	Payload   any
	// DedupeKey, when set, allows only one queued or running job per key.
	DedupeKey string
	After     time.Duration
}

type currentJobKey struct{}

// current returns the job whose handler ctx belongs to, if any.
func current(ctx context.Context) (db.Job, bool) {
	j, ok := ctx.Value(currentJobKey{}).(db.Job)
	return j, ok
}

// Enqueue adds a job and wakes a worker. A handler may enqueue its own
// successor under the same dedupe key: the key passes to the new job.
func (q *Queue) Enqueue(ctx context.Context, o EnqueueOpts) (db.Job, error) {
	if _, ok := q.kinds[o.Kind]; !ok {
		return db.Job{}, fmt.Errorf("unknown job kind %q", o.Kind)
	}
	if cur, ok := current(ctx); ok && o.DedupeKey != "" && cur.DedupeKey.String == o.DedupeKey {
		if err := q.st.Q.ReleaseJobDedupe(context.WithoutCancel(ctx), cur.ID); err != nil {
			return db.Job{}, err
		}
	}
	payload := []byte("{}")
	if o.Payload != nil {
		var err error
		if payload, err = json.Marshal(o.Payload); err != nil {
			return db.Job{}, err
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return db.Job{}, err
	}
	now := store.Now()
	job, err := q.st.Q.EnqueueJob(ctx, db.EnqueueJobParams{
		ID:        id.String(),
		Kind:      o.Kind,
		UnitID:    store.NullString(o.UnitID),
		ProjectID: store.NullString(o.ProjectID),
		Payload:   string(payload),
		DedupeKey: store.NullString(o.DedupeKey),
		RunAfter:  now.Add(o.After),
		Now:       now,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return db.Job{}, ErrDuplicate
		}
		return db.Job{}, err
	}
	q.notify(job)
	q.Wake()
	return job, nil
}

// Wake prods an idle worker to look for work.
func (q *Queue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// PauseClaude holds back Claude jobs until t.
func (q *Queue) PauseClaude(t time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t.After(q.pausedUntil) {
		q.pausedUntil = t
	}
}

// PausedUntil reports when Claude jobs resume; zero if they are not paused.
func (q *Queue) PausedUntil() time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	if time.Now().After(q.pausedUntil) {
		return time.Time{}
	}
	return q.pausedUntil
}

// CancelUnit cancels the job running for a unit, if any, and drops its
// queued jobs. It reports whether a running job was cancelled.
func (q *Queue) CancelUnit(ctx context.Context, unitID string) bool {
	_ = q.st.Q.CancelQueuedJobsForUnit(ctx, db.CancelQueuedJobsForUnitParams{Now: store.NowNull(), UnitID: store.NullString(unitID)})
	q.mu.Lock()
	defer q.mu.Unlock()
	jobID, ok := q.unitJob[unitID]
	if !ok {
		return false
	}
	q.cancelled[jobID] = true
	q.running[jobID]()
	return true
}

// Sweep reconciles jobs left running by a previous process: safe ones are
// re-queued, side-effect ones are marked interrupted and reported. Call once
// before Run.
func (q *Queue) Sweep(ctx context.Context) error {
	jobs, err := q.st.Q.ListRunningJobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		k := q.kinds[j.Kind]
		if k.SideEffects {
			if err := q.st.Q.FinishJob(ctx, db.FinishJobParams{Status: StatusInterrupted, Error: "thefactory stopped while this job was running", Now: store.NowNull(), ID: j.ID}); err != nil {
				return err
			}
			q.log.Warn("job interrupted by restart", "job", j.ID, "kind", j.Kind)
			if q.OnInterrupted != nil {
				q.OnInterrupted(ctx, j)
			}
			continue
		}
		if err := q.st.Q.RequeueJob(ctx, db.RequeueJobParams{RunAfter: store.Now(), Error: "requeued after restart", ID: j.ID}); err != nil {
			return err
		}
		q.log.Info("job requeued after restart", "job", j.ID, "kind", j.Kind)
	}
	return nil
}

// Run starts the workers and blocks until ctx is done and they have stopped.
func (q *Queue) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := range q.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.work(ctx, i)
		}()
	}
	wg.Wait()
}

func (q *Queue) work(ctx context.Context, worker int) {
	for ctx.Err() == nil {
		job, ok := q.claim(ctx)
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			case <-time.After(time.Second):
			}
			continue
		}
		q.execute(ctx, worker, job)
		// Another job may be ready; let the next idle worker look too.
		q.Wake()
	}
}

func (q *Queue) claim(ctx context.Context) (db.Job, bool) {
	q.claimMu.Lock()
	defer q.claimMu.Unlock()
	paused := !q.PausedUntil().IsZero()
	kinds := make([]string, 0, len(q.kinds))
	for name, k := range q.kinds {
		if paused && k.Claude {
			continue
		}
		kinds = append(kinds, name)
	}
	if len(kinds) == 0 {
		return db.Job{}, false
	}
	job, err := q.st.Q.ClaimJob(ctx, db.ClaimJobParams{Now: store.NowNull(), Kinds: kinds})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			q.log.Error("claim job", "error", err)
		}
		return db.Job{}, false
	}
	return job, true
}

func (q *Queue) execute(parent context.Context, worker int, job db.Job) {
	ctx, cancel := context.WithCancel(context.WithValue(parent, currentJobKey{}, job))
	defer cancel()
	q.mu.Lock()
	q.running[job.ID] = cancel
	if job.UnitID.Valid {
		q.unitJob[job.UnitID.String] = job.ID
	}
	q.mu.Unlock()
	q.notify(job)

	log := q.log.With("job", job.ID, "kind", job.Kind, "worker", worker)
	log.Info("job started")
	err := q.call(ctx, job)

	q.mu.Lock()
	delete(q.running, job.ID)
	if job.UnitID.Valid && q.unitJob[job.UnitID.String] == job.ID {
		delete(q.unitJob, job.UnitID.String)
	}
	wasCancelled := q.cancelled[job.ID]
	delete(q.cancelled, job.ID)
	q.mu.Unlock()

	// Bookkeeping must happen even when the server is shutting down.
	bg := context.WithoutCancel(parent)
	var retry *RetryError
	status, msg := StatusDone, ""
	switch {
	case wasCancelled:
		status = StatusCancelled
	case parent.Err() != nil:
		// Shutting down: leave it running so the next boot's sweep decides.
		log.Info("job stopped by shutdown")
		return
	case errors.As(err, &retry):
		if rerr := q.st.Q.RequeueJob(bg, db.RequeueJobParams{RunAfter: store.Now().Add(retry.After), Error: retry.Error(), ID: job.ID}); rerr != nil {
			log.Error("requeue job", "error", rerr)
		}
		log.Info("job requeued", "after", retry.After, "error", retry.Err)
		return
	case err != nil:
		status, msg = StatusFailed, err.Error()
	}
	if ferr := q.st.Q.FinishJob(bg, db.FinishJobParams{Status: status, Error: msg, Now: store.NowNull(), ID: job.ID}); ferr != nil {
		log.Error("finish job", "error", ferr)
	}
	job.Status, job.Error = status, msg
	q.notify(job)
	if err != nil && !wasCancelled {
		log.Warn("job failed", "error", err)
	} else {
		log.Info("job finished", "status", status)
	}
}

func (q *Queue) call(ctx context.Context, job db.Job) (err error) {
	h := q.kinds[job.Kind].Handler
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("job panicked: %v", r)
			q.log.Error("job panicked", "job", job.ID, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	return h(ctx, job)
}

func (q *Queue) notify(job db.Job) {
	if q.OnChange != nil {
		q.OnChange(job)
	}
}

// Cancelled reports whether ctx was cancelled by CancelUnit rather than by
// shutdown; handlers use it to word their outcome.
func Cancelled(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.Canceled)
}
