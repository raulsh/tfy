package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/prompts"
	"github.com/raulsh/tfy/internal/slack"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// Feedback triage statuses.
const (
	FeedbackNew       = "new"      // waiting for auto-triage
	FeedbackTriaging  = "triaging" // in a triage run
	FeedbackProposal  = "proposal" // behind a proposed unit
	FeedbackAttached  = "attached" // linked to a unit
	FeedbackNoise     = "noise"
	FeedbackUncertain = "uncertain" // triage was unsure; a person decides
	FeedbackDismissed = "dismissed"
	FeedbackInbox     = "inbox" // not auto-triaged (backfill, or triage off)
)

const (
	// triageBatch bounds how many messages one triage run judges.
	triageBatch = 20
	// threadWindow is how far back new thread replies are looked for.
	threadWindow = "72h"
	threadSweep  = 10 * time.Minute
)

// SourceInput adds or changes a Slack source.
type SourceInput struct {
	ChannelID     string `json:"channel_id"`
	ChannelName   string `json:"channel_name"`
	AutoTriage    *bool  `json:"auto_triage"`
	ExcludeBots   *bool  `json:"exclude_bots"`
	PollIntervalS int    `json:"poll_interval_s"`
	// BackfillDays imports that many days of history into the inbox,
	// without auto-triage, when the source is added.
	BackfillDays int `json:"backfill_days"`
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func (p *Pipeline) slackClient() (*slack.Client, error) {
	if p.Slack == nil {
		return nil, &ConflictError{Msg: "slk is not available: install it and run `slk configure`"}
	}
	return p.Slack, nil
}

// slackTS renders a time as a Slack ts, which slk accepts as a bound.
func slackTS(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

// AddSource starts watching a channel for a project. A channel feeds one
// project only.
func (p *Pipeline) AddSource(ctx context.Context, projectID string, in SourceInput) (db.SlackSource, error) {
	if _, err := p.slackClient(); err != nil {
		return db.SlackSource{}, err
	}
	if in.ChannelID == "" {
		return db.SlackSource{}, &InvalidError{Msg: "pick a channel"}
	}
	if _, err := p.Store.Q.GetProject(ctx, projectID); err != nil {
		return db.SlackSource{}, &NotFoundError{What: "project"}
	}
	interval := max(in.PollIntervalS, 60)
	if in.PollIntervalS == 0 {
		interval = int(p.Config.SlackPollInterval.Seconds())
	}
	now := time.Now()
	src, err := p.Store.Q.CreateSlackSource(ctx, db.CreateSlackSourceParams{
		ID: newID(), ProjectID: projectID, ChannelID: in.ChannelID, ChannelName: strings.TrimPrefix(in.ChannelName, "#"),
		AutoTriage: boolOr(in.AutoTriage, true), ExcludeBots: boolOr(in.ExcludeBots, true), PollIntervalS: int64(interval),
		// Only messages from now on are triaged.
		CursorTs: slackTS(now), Now: store.Now(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return src, &ConflictError{Msg: "that channel already feeds a project"}
		}
		return src, err
	}
	if in.BackfillDays > 0 {
		since := slackTS(now.AddDate(0, 0, -min(in.BackfillDays, 30)))
		if _, err := p.readHistory(ctx, src, since, slackTS(now), FeedbackInbox); err != nil {
			p.Log.Warn("backfill failed", "channel", src.ChannelName, "error", err)
		}
	}
	p.changed("project", projectID)
	return src, nil
}

// UpdateSource changes how a channel is watched.
func (p *Pipeline) UpdateSource(ctx context.Context, id string, in SourceInput) (db.SlackSource, error) {
	cur, err := p.Store.Q.GetSlackSource(ctx, id)
	if err != nil {
		return cur, err
	}
	interval := cur.PollIntervalS
	if in.PollIntervalS > 0 {
		interval = int64(max(in.PollIntervalS, 60))
	}
	src, err := p.Store.Q.UpdateSlackSource(ctx, db.UpdateSlackSourceParams{
		AutoTriage: boolOr(in.AutoTriage, cur.AutoTriage), ExcludeBots: boolOr(in.ExcludeBots, cur.ExcludeBots),
		PollIntervalS: interval, ID: id,
	})
	if err == nil {
		p.changed("project", src.ProjectID)
	}
	return src, err
}

// RemoveSource stops watching a channel. Its feedback stays.
func (p *Pipeline) RemoveSource(ctx context.Context, id string) error {
	src, err := p.Store.Q.GetSlackSource(ctx, id)
	if err != nil {
		return err
	}
	if err := p.Store.Q.DeleteSlackSource(ctx, id); err != nil {
		return err
	}
	p.changed("project", src.ProjectID)
	return nil
}

// PollSource reads a channel's new messages. The cursor moves only after a
// complete read, so a failure or truncation never skips messages.
func (p *Pipeline) PollSource(ctx context.Context, src db.SlackSource) (int, error) {
	status := FeedbackNew
	if !src.AutoTriage {
		status = FeedbackInbox
	}
	newest, err := p.readHistory(ctx, src, src.CursorTs, "", status)
	lastErr := ""
	if err != nil {
		lastErr = err.Error()
		newest.ts = src.CursorTs
	}
	if serr := p.Store.Q.SetSlackSourcePolled(ctx, db.SetSlackSourcePolledParams{CursorTs: newest.ts, Now: store.NowNull(), LastError: lastErr, ID: src.ID}); serr != nil {
		return newest.count, serr
	}
	if newest.count > 0 {
		p.changed("feedback", src.ProjectID)
	}
	return newest.count, err
}

type readResult struct {
	ts    string // newest ts seen (or the cursor)
	count int    // messages newly stored
}

// readHistory stores the messages in (since, until], paging back through
// slk's truncation until the window is complete.
func (p *Pipeline) readHistory(ctx context.Context, src db.SlackSource, since, until, status string) (readResult, error) {
	cl, err := p.slackClient()
	if err != nil {
		return readResult{ts: since}, err
	}
	res := readResult{ts: since}
	for page := 0; page < 20; page++ {
		msgs, truncated, err := cl.History(ctx, slack.HistoryOptions{Channel: src.ChannelID, Since: since, Until: until, Limit: 500, ExcludeBots: src.ExcludeBots})
		if err != nil {
			return res, err
		}
		oldest := ""
		for _, m := range msgs {
			if m.TS <= since {
				continue // the bound is inclusive
			}
			if m.TS > res.ts {
				res.ts = m.TS
			}
			if oldest == "" || m.TS < oldest {
				oldest = m.TS
			}
			if inserted, err := p.storeMessage(ctx, src.ProjectID, src.ID, m, status); err != nil {
				return res, err
			} else if inserted {
				res.count++
			}
		}
		if !truncated || oldest == "" {
			return res, nil
		}
		until = oldest // older messages were left out: read them next
	}
	return res, errors.New("gave up paging through a very busy channel; the next poll continues")
}

// storeMessage records a Slack message as feedback, reporting whether it is
// new. Re-reads update its text and reply count, never its triage.
func (p *Pipeline) storeMessage(ctx context.Context, projectID, sourceID string, m slack.Message, status string) (bool, error) {
	now := store.Now()
	threadTS := m.ThreadTS
	if threadTS == m.TS {
		threadTS = ""
	}
	fb, err := p.Store.Q.UpsertFeedback(ctx, db.UpsertFeedbackParams{
		ID: newID(), ProjectID: projectID, SourceID: store.NullString(sourceID), ChannelID: m.Channel,
		ChannelName: m.ChannelName, Ts: m.TS, ThreadTs: threadTS, AuthorID: m.User, AuthorName: m.Author(),
		Text: m.Body(), Permalink: m.Permalink, ReplyCount: int64(m.ReplyCount), Edited: m.Edited,
		PostedAt: m.Posted(), TriageStatus: status, Now: now,
	})
	if err != nil {
		return false, err
	}
	return fb.CreatedAt.Equal(now), nil
}

// sweepThreads catches replies to recent threads: an incremental read only
// sees top-level messages newer than the cursor.
func (p *Pipeline) sweepThreads(ctx context.Context, src db.SlackSource) error {
	cl, err := p.slackClient()
	if err != nil {
		return err
	}
	msgs, _, err := cl.History(ctx, slack.HistoryOptions{Channel: src.ChannelID, Since: threadWindow, Limit: 500, ExcludeBots: src.ExcludeBots})
	if err != nil {
		return err
	}
	status := FeedbackNew
	if !src.AutoTriage {
		status = FeedbackInbox
	}
	stored := 0
	for _, m := range msgs {
		if m.ReplyCount == 0 || m.IsReply() {
			continue
		}
		known := int64(0)
		if fb, err := p.Store.Q.GetFeedbackByTS(ctx, db.GetFeedbackByTSParams{ChannelID: m.Channel, Ts: m.TS}); err == nil {
			known = fb.ReplyCount
		}
		if int64(m.ReplyCount) <= known {
			continue
		}
		thread, err := cl.Thread(ctx, m.Channel, m.TS)
		if err != nil {
			return err
		}
		for _, r := range thread {
			st := status
			// A parent older than the source is context, not new feedback.
			if r.TS == m.TS && r.Posted().Before(src.CreatedAt) {
				st = FeedbackInbox
			}
			if r.ChannelName == "" {
				r.ChannelName = src.ChannelName
			}
			if inserted, err := p.storeMessage(ctx, src.ProjectID, src.ID, r, st); err != nil {
				return err
			} else if inserted {
				stored++
			}
		}
	}
	if err := p.Store.Q.SetSlackSourceSwept(ctx, db.SetSlackSourceSweptParams{Now: store.NowNull(), ID: src.ID}); err != nil {
		return err
	}
	if stored > 0 {
		p.changed("feedback", src.ProjectID)
	}
	return nil
}

// pollSlack polls the sources that are due, sweeps threads, and queues
// triage for projects with new feedback.
func (p *Pipeline) pollSlack(ctx context.Context) error {
	if p.Slack == nil {
		return nil
	}
	sources, err := p.Store.Q.ListSlackSources(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, src := range sources {
		if !src.LastPolledAt.Valid || now.Sub(src.LastPolledAt.Time) >= time.Duration(src.PollIntervalS)*time.Second {
			if _, err := p.PollSource(ctx, src); err != nil {
				p.Log.Warn("slack poll failed", "channel", src.ChannelName, "error", err)
				var se *slack.Error
				if errors.As(err, &se) && se.RetryAfter > 0 {
					return nil // back off: the next tick tries again
				}
			}
		}
		if !src.LastThreadSweepAt.Valid || now.Sub(src.LastThreadSweepAt.Time) >= threadSweep {
			if err := p.sweepThreads(ctx, src); err != nil {
				p.Log.Warn("slack thread sweep failed", "channel", src.ChannelName, "error", err)
			}
		}
	}
	return p.queueTriage(ctx)
}

func (p *Pipeline) queueTriage(ctx context.Context) error {
	projects, err := p.Store.Q.ListProjectsWithNewFeedback(ctx)
	if err != nil {
		return err
	}
	for _, projectID := range projects {
		_, err := p.Jobs.Enqueue(ctx, jobs.EnqueueOpts{Kind: JobTriage, ProjectID: projectID, DedupeKey: "triage:" + projectID})
		if err != nil && !errors.Is(err, jobs.ErrDuplicate) {
			return err
		}
	}
	return nil
}

// triageItem is one verdict of a triage run.
type triageItem struct {
	ID         string  `json:"id"`
	Verdict    string  `json:"verdict"`
	GroupKey   string  `json:"group_key"`
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	Summary    string  `json:"summary"`
	Unit       string  `json:"unit"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// triage judges a project's new feedback in one run: noise is set aside,
// messages about tracked work are attached to it, and the rest become
// proposals a person accepts.
func (p *Pipeline) triage(ctx context.Context, job db.Job) error {
	projectID := job.ProjectID.String
	batch, err := p.Store.Q.ListNewFeedback(ctx, db.ListNewFeedbackParams{ProjectID: projectID, Lim: triageBatch})
	if err != nil || len(batch) == 0 {
		return err
	}
	ids := make([]string, len(batch))
	for i, f := range batch {
		ids[i] = f.ID
	}
	if err := p.Store.Q.SetFeedbackStatus(ctx, db.SetFeedbackStatusParams{TriageStatus: FeedbackTriaging, Now: store.Now(), Ids: ids}); err != nil {
		return err
	}
	project, err := p.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	settings := domain.ParseProjectSettings(project.Settings)

	units, err := p.Store.Q.ListUnits(ctx, db.ListUnitsParams{ProjectID: projectID, Lim: 200})
	if err != nil {
		return err
	}
	data := prompts.Triage{Project: project.Name, About: project.Description, ProductContext: project.ProductContext}
	for _, u := range units {
		if domain.State(u.State).Terminal() || len(data.Units) >= 50 {
			continue
		}
		data.Units = append(data.Units, prompts.TriageUnit{Label: domain.Label(u.Seq), State: domain.State(u.State).Label(), Title: u.Title, Summary: u.Summary})
	}
	inBatch := map[string]bool{}
	for _, f := range batch {
		inBatch[f.ChannelID+"/"+f.Ts] = true
	}
	seenParent := map[string]bool{}
	for _, f := range batch {
		msg := prompts.TriageMessage{ID: f.ID, Author: f.AuthorName, At: f.PostedAt.Format("2006-01-02 15:04"), Channel: f.ChannelName, Text: f.Text}
		if f.ThreadTs != "" {
			if parent, err := p.Store.Q.GetFeedbackByTS(ctx, db.GetFeedbackByTSParams{ChannelID: f.ChannelID, Ts: f.ThreadTs}); err == nil {
				msg.InReplyTo = parent.ID
				key := parent.ChannelID + "/" + parent.Ts
				if !inBatch[key] && !seenParent[key] {
					seenParent[key] = true
					data.Parents = append(data.Parents, prompts.TriageMessage{ID: parent.ID, Author: parent.AuthorName, Text: parent.Text})
				}
			}
		}
		data.Messages = append(data.Messages, msg)
	}

	req := runRequest{Kind: "triage", ProjectID: projectID, Schema: prompts.Schema("triage"), Cwd: filepath.Join(p.Paths.Root, "triage")}
	if err := os.MkdirAll(req.Cwd, 0o755); err != nil {
		return err
	}
	if req.Prompt, req.PromptVersion, err = prompts.Render("triage", data); err != nil {
		return err
	}
	run, out, err := p.runClaude(ctx, req)
	if err == nil && out.Status != "succeeded" {
		if out.Status == "rate_limited" {
			_ = p.Store.Q.SetFeedbackStatus(ctx, db.SetFeedbackStatusParams{TriageStatus: FeedbackNew, Now: store.Now(), Ids: ids})
			until := time.Now().Add(15 * time.Minute)
			if rl := out.RateLimit; rl != nil && rl.ResetsAt > 0 {
				until = time.Unix(rl.ResetsAt, 0)
			}
			p.Jobs.PauseClaude(until)
			return &jobs.RetryError{After: time.Until(until) + time.Minute, Err: errors.New("rate limited")}
		}
		err = fmt.Errorf("triage run %s: %s", out.Status, out.Reason)
	}
	var result struct {
		Items []triageItem `json:"items"`
	}
	if err == nil {
		if jerr := json.Unmarshal([]byte(run.Result), &result); jerr != nil {
			err = fmt.Errorf("triage returned no usable verdicts: %w", jerr)
		}
	}
	if err != nil {
		// Leave the decision to a person rather than retrying forever.
		for _, f := range batch {
			p.setTriage(ctx, f, FeedbackUncertain, triageItem{Reason: "triage failed: " + err.Error()}, "")
		}
		p.changed("feedback", projectID)
		return err
	}
	return p.applyTriage(ctx, project, settings, batch, result.Items, run.ID)
}

func (p *Pipeline) setTriage(ctx context.Context, f db.Feedback, status string, it triageItem, unitID string) {
	raw, _ := json.Marshal(it)
	if err := p.Store.Q.SetFeedbackTriage(ctx, db.SetFeedbackTriageParams{
		TriageStatus: status, Triage: string(raw), UnitID: store.NullString(unitID), Now: store.Now(), ID: f.ID,
	}); err != nil {
		p.Log.Error("record triage", "feedback", f.ID, "error", err)
	}
}

func (p *Pipeline) applyTriage(ctx context.Context, project db.Project, settings domain.ProjectSettings, batch []db.Feedback, items []triageItem, runID string) error {
	byID := map[string]triageItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	units, _ := p.Store.Q.ListUnits(ctx, db.ListUnitsParams{ProjectID: project.ID, Lim: 1000})
	bySeq := map[string]db.Unit{}
	for _, u := range units {
		bySeq[domain.Label(u.Seq)] = u
	}

	type member struct {
		f  db.Feedback
		it triageItem
	}
	groups := map[string][]member{}
	var order []string
	attached := map[string]int{}
	for _, f := range batch {
		it, ok := byID[f.ID]
		switch {
		case !ok:
			p.setTriage(ctx, f, FeedbackUncertain, triageItem{Reason: "triage did not return a verdict for this message"}, "")
		case it.Verdict == "noise":
			p.setTriage(ctx, f, FeedbackNoise, it, "")
		case it.Confidence < settings.TriageConfidenceMin:
			p.setTriage(ctx, f, FeedbackUncertain, it, "")
		case it.Verdict == "attach":
			u, ok := bySeq[strings.ToUpper(strings.TrimSpace(it.Unit))]
			if !ok || domain.State(u.State).Terminal() {
				p.setTriage(ctx, f, FeedbackUncertain, it, "")
				continue
			}
			p.setTriage(ctx, f, FeedbackAttached, it, u.ID)
			attached[u.ID]++
		default:
			key := strings.TrimSpace(it.GroupKey)
			if key == "" {
				key = f.ID
			}
			if _, seen := groups[key]; !seen {
				order = append(order, key)
			}
			groups[key] = append(groups[key], member{f, it})
		}
	}

	for unitID, n := range attached {
		u, err := p.Store.Q.GetUnit(ctx, unitID)
		if err != nil {
			continue
		}
		p.activity(ctx, u.ID, "triage", "feedback", fmt.Sprintf("%d new Slack message(s) attached", n), map[string]string{"run_id": runID})
		if u.State != string(domain.StateProposed) && u.Attention == "" {
			p.flag(ctx, u.ID, domain.AttentionNewFeedback, fmt.Sprintf("%d new Slack message(s) about this unit; see Overview", n))
		}
	}

	for _, key := range order {
		members := groups[key]
		best := members[0].it
		var text strings.Builder
		for _, m := range members {
			if m.it.Confidence > best.Confidence {
				best = m.it
			}
			fmt.Fprintf(&text, "> %s (#%s): %s\n\n", m.f.AuthorName, m.f.ChannelName, strings.ReplaceAll(m.f.Text, "\n", "\n> "))
		}
		kind, err := domain.ParseKind(best.Kind)
		if err != nil {
			kind = domain.KindFeature
		}
		title := strings.TrimSpace(best.Title)
		if title == "" {
			title = clip(members[0].f.Text, 70)
		}
		u, err := p.newUnit(ctx, unitSpec{
			ProjectID: project.ID, Kind: kind, Title: title, Summary: best.Summary, Description: text.String(),
			Origin: domain.OriginSlackAuto, State: domain.StateProposed, CreatedBy: "triage",
		})
		if err != nil {
			return err
		}
		for _, m := range members {
			p.setTriage(ctx, m.f, FeedbackProposal, m.it, u.ID)
		}
		p.activity(ctx, u.ID, "triage", "feedback", fmt.Sprintf("proposed from %d Slack message(s): %s", len(members), best.Reason), map[string]string{"run_id": runID})
		if settings.AutoAcceptProposals {
			if _, err := p.Act(ctx, u.ID, ActionAccept, ActionInput{Actor: "triage"}); err != nil {
				p.Log.Warn("auto-accept proposal", "unit", domain.Label(u.Seq), "error", err)
			}
		}
	}
	p.changed("feedback", project.ID)
	return nil
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// FromFeedbackInput creates a unit from Slack messages a person picked.
type FromFeedbackInput struct {
	ProjectID   string   `json:"project_id"`
	FeedbackIDs []string `json:"feedback_ids"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	CreatedBy   string   `json:"created_by"`
	// RunOverrides choose other models or effort levels for the unit's
	// runs than the configuration's.
	RunOverrides domain.RunOverrides `json:"run_overrides"`
	// Subagents, when false, keeps the unit's runs from starting
	// sub-agents.
	Subagents *bool `json:"subagents,omitempty"`
}

// CreateUnitFromFeedback groups messages into one unit and starts defining
// it. Threads are read in full, so the define run sees the whole discussion.
func (p *Pipeline) CreateUnitFromFeedback(ctx context.Context, in FromFeedbackInput) (db.Unit, error) {
	if len(in.FeedbackIDs) == 0 {
		return db.Unit{}, &InvalidError{Msg: "pick at least one message"}
	}
	fbs, err := p.Store.Q.ListFeedbackByIDs(ctx, in.FeedbackIDs)
	if err != nil {
		return db.Unit{}, err
	}
	if len(fbs) == 0 {
		return db.Unit{}, &NotFoundError{What: "feedback"}
	}
	projectID := in.ProjectID
	if projectID == "" {
		projectID = fbs[0].ProjectID
	}
	ids := []string{}
	for _, f := range fbs {
		if f.ProjectID != projectID {
			return db.Unit{}, &InvalidError{Msg: "the messages belong to different projects"}
		}
		ids = append(ids, f.ID)
		if f.ReplyCount > 0 && f.ThreadTs == "" && p.Slack != nil {
			thread, err := p.Slack.Thread(ctx, f.ChannelID, f.Ts)
			if err != nil {
				p.Log.Warn("read thread", "channel", f.ChannelName, "ts", f.Ts, "error", err)
				continue
			}
			for _, r := range thread {
				if r.TS == f.Ts {
					continue
				}
				if r.ChannelName == "" {
					r.ChannelName = f.ChannelName
				}
				if _, err := p.storeMessage(ctx, projectID, f.SourceID.String, r, FeedbackInbox); err != nil {
					return db.Unit{}, err
				}
				if reply, err := p.Store.Q.GetFeedbackByTS(ctx, db.GetFeedbackByTSParams{ChannelID: r.Channel, Ts: r.TS}); err == nil && !slices.Contains(ids, reply.ID) {
					ids = append(ids, reply.ID)
				}
			}
		}
	}
	kind, err := domain.ParseKind(in.Kind)
	if err != nil {
		return db.Unit{}, &InvalidError{Msg: err.Error()}
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = clip(fbs[0].Text, 70)
	}
	u, err := p.newUnit(ctx, unitSpec{
		ProjectID: projectID, Kind: kind, Title: title, Description: in.Description,
		Origin: domain.OriginSlackManual, State: domain.StateDefining, CreatedBy: in.CreatedBy, RunOverrides: in.RunOverrides,
		NoSubagents: off(in.Subagents),
	})
	if err != nil {
		return u, err
	}
	if err := p.Store.Q.LinkFeedback(ctx, db.LinkFeedbackParams{UnitID: store.NullString(u.ID), TriageStatus: FeedbackAttached, Now: store.Now(), Ids: ids}); err != nil {
		return u, err
	}
	p.activity(ctx, u.ID, actorOr(in.CreatedBy), "feedback", fmt.Sprintf("created from %d Slack message(s)", len(ids)), nil)
	p.changed("feedback", projectID)
	return u, p.enqueue(ctx, JobDefine, u, definePayload{})
}

// ImportPermalinks reads Slack threads by link into a project's inbox.
func (p *Pipeline) ImportPermalinks(ctx context.Context, projectID string, links []string) ([]db.Feedback, error) {
	cl, err := p.slackClient()
	if err != nil {
		return nil, err
	}
	if _, err := p.Store.Q.GetProject(ctx, projectID); err != nil {
		return nil, &NotFoundError{What: "project"}
	}
	var out []db.Feedback
	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		msgs, err := cl.ThreadByPermalink(ctx, link)
		if err != nil {
			return out, &InvalidError{Msg: fmt.Sprintf("%s: %v", link, err)}
		}
		for _, m := range msgs {
			if _, err := p.storeMessage(ctx, projectID, "", m, FeedbackInbox); err != nil {
				return out, err
			}
			if fb, err := p.Store.Q.GetFeedbackByTS(ctx, db.GetFeedbackByTSParams{ChannelID: m.Channel, Ts: m.TS}); err == nil {
				out = append(out, fb)
			}
		}
	}
	p.changed("feedback", projectID)
	return out, nil
}

// SetFeedbackStatus dismisses feedback or brings it back to the inbox.
func (p *Pipeline) SetFeedbackStatus(ctx context.Context, id, status string) (db.Feedback, error) {
	switch status {
	case FeedbackDismissed, FeedbackInbox:
	default:
		return db.Feedback{}, &InvalidError{Msg: "feedback can be dismissed or restored to the inbox"}
	}
	f, err := p.Store.Q.GetFeedback(ctx, id)
	if err != nil {
		return f, err
	}
	if f.UnitID.Valid {
		return f, &ConflictError{Msg: "this message belongs to a unit; reject the unit instead"}
	}
	if err := p.Store.Q.SetFeedbackStatus(ctx, db.SetFeedbackStatusParams{TriageStatus: status, Now: store.Now(), Ids: []string{id}}); err != nil {
		return f, err
	}
	p.changed("feedback", f.ProjectID)
	return p.Store.Q.GetFeedback(ctx, id)
}

// unitFeedback is the Slack feedback behind a unit, for its define prompt.
func (p *Pipeline) unitFeedback(ctx context.Context, u db.Unit) []prompts.Feedback {
	fbs, err := p.Store.Q.ListFeedbackByUnit(ctx, store.NullString(u.ID))
	if err != nil {
		return nil
	}
	out := make([]prompts.Feedback, 0, len(fbs))
	for _, f := range fbs {
		out = append(out, prompts.Feedback{Author: f.AuthorName, At: f.PostedAt.Format("2006-01-02 15:04"), Channel: f.ChannelName, Text: f.Text})
	}
	return out
}
