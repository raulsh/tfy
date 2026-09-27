package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

func newQueue(t *testing.T, workers int) (*Queue, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, workers, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func status(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	j, err := st.Q.GetJob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j.Status
}

func TestRunsJobsAndRecordsOutcome(t *testing.T) {
	q, st := newQueue(t, 2)
	var ran atomic.Int32
	q.Register("ok", Kind{Handler: func(context.Context, db.Job) error { ran.Add(1); return nil }})
	q.Register("bad", Kind{Handler: func(context.Context, db.Job) error { return errors.New("nope") }})
	q.Register("boom", Kind{Handler: func(context.Context, db.Job) error { panic("kaboom") }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	ok, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "ok"})
	bad, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "bad"})
	boom, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "boom"})
	waitFor(t, "jobs to finish", func() bool {
		return status(t, st, ok.ID) == StatusDone && status(t, st, bad.ID) == StatusFailed && status(t, st, boom.ID) == StatusFailed
	})
	if j, _ := st.Q.GetJob(ctx, boom.ID); j.Error == "" {
		t.Error("a panic must be recorded as the failure")
	}
}

func TestDedupe(t *testing.T) {
	q, _ := newQueue(t, 1)
	q.Register("k", Kind{Handler: func(context.Context, db.Job) error { return nil }})
	ctx := context.Background()
	if _, err := q.Enqueue(ctx, EnqueueOpts{Kind: "k", DedupeKey: "unit:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, EnqueueOpts{Kind: "k", DedupeKey: "unit:1"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second enqueue: %v, want ErrDuplicate", err)
	}
	if _, err := q.Enqueue(ctx, EnqueueOpts{Kind: "k", DedupeKey: "unit:2"}); err != nil {
		t.Fatal(err)
	}
}

func TestDedupeFreesUpAfterFinish(t *testing.T) {
	q, st := newQueue(t, 1)
	q.Register("k", Kind{Handler: func(context.Context, db.Job) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	j, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "k", DedupeKey: "unit:1"})
	waitFor(t, "job", func() bool { return status(t, st, j.ID) == StatusDone })
	if _, err := q.Enqueue(ctx, EnqueueOpts{Kind: "k", DedupeKey: "unit:1"}); err != nil {
		t.Fatalf("a finished job must not block a new one: %v", err)
	}
}

func TestCancelUnit(t *testing.T) {
	q, st := newQueue(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := store.Now()
	if _, err := st.Q.CreateProject(ctx, db.CreateProjectParams{ID: "p1", Name: "P", Slug: "p", Settings: "{}", Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.CreateUnit(ctx, db.CreateUnitParams{ID: "u1", Seq: 1, ProjectID: "p1", Kind: "feature", Title: "t", Origin: "developer", State: "defining", Now: now}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	q.Register("slow", Kind{Handler: func(ctx context.Context, _ db.Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	go q.Run(ctx)
	j, err := q.Enqueue(ctx, EnqueueOpts{Kind: "slow", UnitID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if q.CancelUnit(ctx, "nope") {
		t.Fatal("no job runs for that unit")
	}
	if !q.CancelUnit(ctx, "u1") {
		t.Fatal("the running job was not cancelled")
	}
	waitFor(t, "cancellation", func() bool { return status(t, st, j.ID) == StatusCancelled })
}

func TestRetry(t *testing.T) {
	q, st := newQueue(t, 1)
	var n atomic.Int32
	q.Register("flaky", Kind{Handler: func(context.Context, db.Job) error {
		if n.Add(1) == 1 {
			return &RetryError{After: 10 * time.Millisecond, Err: errors.New("try again")}
		}
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	j, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "flaky"})
	waitFor(t, "retry", func() bool { return status(t, st, j.ID) == StatusDone })
	if got, _ := st.Q.GetJob(ctx, j.ID); got.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", got.Attempts)
	}
}

func TestSweep(t *testing.T) {
	q, st := newQueue(t, 1)
	q.Register("read", Kind{Handler: func(context.Context, db.Job) error { return nil }})
	q.Register("publish", Kind{Handler: func(context.Context, db.Job) error { return nil }, SideEffects: true})
	ctx := context.Background()
	r, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "read"})
	p, _ := q.Enqueue(ctx, EnqueueOpts{Kind: "publish"})
	// Simulate a crash mid-run: both claimed, neither finished.
	for range 2 {
		if _, ok := q.claim(ctx); !ok {
			t.Fatal("claim failed")
		}
	}
	var interrupted []string
	q.OnInterrupted = func(_ context.Context, j db.Job) { interrupted = append(interrupted, j.ID) }
	if err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if s := status(t, st, r.ID); s != StatusQueued {
		t.Errorf("read-only job after sweep = %s, want queued", s)
	}
	if s := status(t, st, p.ID); s != StatusInterrupted {
		t.Errorf("side-effect job after sweep = %s, want interrupted", s)
	}
	if len(interrupted) != 1 || interrupted[0] != p.ID {
		t.Errorf("OnInterrupted got %v", interrupted)
	}
}

func TestPauseHoldsBackClaudeJobs(t *testing.T) {
	q, _ := newQueue(t, 1)
	q.Register("claude", Kind{Handler: func(context.Context, db.Job) error { return nil }, Claude: true})
	q.Register("git", Kind{Handler: func(context.Context, db.Job) error { return nil }})
	ctx := context.Background()
	_, _ = q.Enqueue(ctx, EnqueueOpts{Kind: "claude"})
	q.PauseClaude(time.Now().Add(time.Hour))
	if _, ok := q.claim(ctx); ok {
		t.Fatal("a Claude job was claimed while paused")
	}
	_, _ = q.Enqueue(ctx, EnqueueOpts{Kind: "git"})
	if j, ok := q.claim(ctx); !ok || j.Kind != "git" {
		t.Fatal("non-Claude jobs keep running while paused")
	}
}
