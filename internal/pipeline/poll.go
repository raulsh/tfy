package pipeline

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// prStates are the unit states whose pull requests are watched.
var prStates = []string{string(domain.StateReviewing), string(domain.StateAwaitingMerge), string(domain.StateMerging)}

// Start runs the background pollers until ctx is done.
func (p *Pipeline) Start(ctx context.Context) {
	go p.every(ctx, p.Config.PRPollInterval, "pull requests", p.pollPRs)
	if p.Slack != nil {
		// Each source keeps its own interval; the tick only checks who is due.
		go p.every(ctx, 20*time.Second, "slack", p.pollSlack)
	}
}

func (p *Pipeline) every(ctx context.Context, d time.Duration, what string, f func(context.Context) error) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		if err := f(ctx); err != nil && ctx.Err() == nil {
			p.Log.Warn("poll failed", "what", what, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Pipeline) pollPRs(ctx context.Context) error {
	units, err := p.Store.Q.ListUnitsInStates(ctx, prStates)
	if err != nil {
		return err
	}
	for _, u := range units {
		if err := p.pollUnitPRs(ctx, u); err != nil {
			p.Log.Warn("poll pull requests", "unit", domain.Label(u.Seq), "error", err)
		}
	}
	return nil
}

// pollUnitPRs refreshes a unit's pull requests and moves the unit on when
// they have all been merged, wherever that happened. A unit the merge run
// is merging moves on through the merge job instead.
func (p *Pipeline) pollUnitPRs(ctx context.Context, u db.Unit) error {
	urs, err := p.syncPRs(ctx, u)
	if err != nil || u.State == string(domain.StateMerging) {
		return err
	}
	withPR, merged, closed := 0, 0, []string{}
	for _, ur := range targetsOf(urs) {
		if ur.PrNumber == 0 {
			continue
		}
		withPR++
		switch ur.PrState {
		case "merged":
			merged++
		case "closed":
			closed = append(closed, fmt.Sprintf("%s #%d", ur.FullName, ur.PrNumber))
		case "open":
			// Commits pushed to the pull request after it was reviewed.
			if u.State == string(domain.StateAwaitingMerge) && ur.ReviewedSha != "" && ur.HeadSha != ur.ReviewedSha &&
				u.Attention != string(domain.AttentionHeadChanged) {
				p.flag(ctx, u.ID, domain.AttentionHeadChanged, fmt.Sprintf("%s #%d has commits the review did not see; review it again before merging", ur.FullName, ur.PrNumber))
			}
		}
	}
	switch {
	case withPR > 0 && merged == withPR:
		u, err := p.transition(ctx, u, domain.StateReleasing, "github", "all pull requests merged")
		if err != nil {
			return err
		}
		return p.enqueue(ctx, JobRelease, u, nil)
	case len(closed) > 0 && u.Attention != string(domain.AttentionPRClosed):
		p.flag(ctx, u.ID, domain.AttentionPRClosed, "closed without merging: "+strings.Join(closed, ", "))
	}
	return nil
}

// syncPRs records where each of the unit's pull requests stands on GitHub,
// and returns the unit's repositories as they are then.
func (p *Pipeline) syncPRs(ctx context.Context, u db.Unit) ([]db.ListUnitReposRow, error) {
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, ur := range targetsOf(urs) {
		if ur.PrNumber == 0 {
			continue
		}
		pr, err := p.GH.PRView(ctx, ur.FullName, int(ur.PrNumber))
		if err != nil {
			return nil, err
		}
		state := strings.ToLower(pr.State)
		var mergedAt time.Time
		if pr.MergedAt != nil {
			mergedAt = pr.MergedAt.UTC()
		}
		head := pr.HeadRefOid
		if head == "" {
			head = ur.HeadSha
		}
		if err := p.Store.Q.SetUnitRepoPRStatus(ctx, db.SetUnitRepoPRStatusParams{
			PrState: state, ChecksState: pr.ChecksState(), HeadSha: head, MergeSha: pr.MergeSHA(),
			MergedAt: store.NullTime(mergedAt), Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID,
		}); err != nil {
			return nil, err
		}
		if state != ur.PrState {
			p.activity(ctx, u.ID, "github", "pr", fmt.Sprintf("%s #%d is %s", ur.FullName, pr.Number, state), map[string]string{"url": pr.URL})
			p.changed("unit", u.ID)
		}
	}
	return p.Store.Q.ListUnitRepos(ctx, u.ID)
}

// Recover reconciles state left by a previous process. Call once at boot,
// before the job queue starts.
func (p *Pipeline) Recover(ctx context.Context) error {
	runs, err := p.Store.Q.ListLiveRuns(ctx)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Pid > 0 && isClaude(int(r.Pid)) {
			_ = syscall.Kill(-int(r.Pid), syscall.SIGKILL)
			p.Log.Warn("killed a run left over from the last process", "run", r.ID, "pid", r.Pid)
		}
		if err := p.Store.Q.InterruptRun(ctx, db.InterruptRunParams{Reason: "tfy stopped during the run", Now: store.NowNull(), ID: r.ID}); err != nil {
			return err
		}
	}
	if err := p.Jobs.Sweep(ctx); err != nil {
		return err
	}
	if err := p.Store.Q.ResetTriagingFeedback(ctx); err != nil {
		return err
	}
	// A unit in a working state with nothing queued would otherwise wait
	// forever (a crash between a transition and its enqueue).
	working := make([]string, 0, len(jobForState))
	for s := range jobForState {
		working = append(working, string(s))
	}
	units, err := p.Store.Q.ListUnitsInStates(ctx, working)
	if err != nil {
		return err
	}
	for _, u := range units {
		if u.Attention == "" && !p.Busy(ctx, u.ID) {
			p.flag(ctx, u.ID, domain.AttentionInterrupted, "tfy stopped before this step started; retry it")
		}
	}
	return nil
}

// isClaude checks a pid still belongs to a claude process, so a recycled pid
// is never killed.
func isClaude(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "claude")
}
