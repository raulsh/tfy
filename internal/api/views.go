package api

import (
	"encoding/json"
	"time"

	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/store/db"
)

// Views are the JSON shapes the UI consumes: database rows without SQL
// null wrappers, plus derived fields.

type UnitView struct {
	ID              string    `json:"id"`
	Seq             int64     `json:"seq"`
	Label           string    `json:"label"`
	ProjectID       string    `json:"project_id"`
	ProjectName     string    `json:"project_name,omitempty"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Summary         string    `json:"summary"`
	Description     string    `json:"description"`
	Origin          string    `json:"origin"`
	State           string    `json:"state"`
	Stage           string    `json:"stage"`
	Attention       string    `json:"attention"`
	AttentionDetail string    `json:"attention_detail"`
	ReviewIteration int64     `json:"review_iteration"`
	WorkspacePath   string    `json:"workspace_path"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Busy            bool      `json:"busy"`
	Actions         []string  `json:"actions"`
}

func unitView(u db.Unit, busy bool, actions []string, projectName string) UnitView {
	return UnitView{
		ID: u.ID, Seq: u.Seq, Label: domain.Label(u.Seq), ProjectID: u.ProjectID, ProjectName: projectName,
		Kind: u.Kind, Title: u.Title, Summary: u.Summary, Description: u.Description, Origin: u.Origin,
		State: u.State, Stage: string(domain.State(u.State).Stage()), Attention: u.Attention,
		AttentionDetail: u.AttentionDetail, ReviewIteration: u.ReviewIteration, WorkspacePath: u.WorkspacePath,
		CreatedBy: u.CreatedBy, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, Busy: busy, Actions: actions,
	}
}

type UnitRepoView struct {
	RepoID        string          `json:"repo_id"`
	FullName      string          `json:"full_name"`
	DefaultBranch string          `json:"default_branch"`
	CheckoutPath  string          `json:"checkout_path"`
	BaseSha       string          `json:"base_sha"`
	IsTarget      bool            `json:"is_target"`
	Branch        string          `json:"branch"`
	HeadSha       string          `json:"head_sha"`
	PublishState  string          `json:"publish_state"`
	PrNumber      int64           `json:"pr_number"`
	PrURL         string          `json:"pr_url"`
	PrState       string          `json:"pr_state"`
	ChecksState   string          `json:"checks_state"`
	MergeSha      string          `json:"merge_sha"`
	MergedAt      *time.Time      `json:"merged_at"`
	ReleaseState  string          `json:"release_state"`
	ReleaseRuns   json.RawMessage `json:"release_runs"`
}

func unitRepoView(r db.ListUnitReposRow) UnitRepoView {
	v := UnitRepoView{
		RepoID: r.RepoID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, CheckoutPath: r.CheckoutPath,
		BaseSha: r.BaseSha, IsTarget: r.IsTarget, Branch: r.Branch, HeadSha: r.HeadSha, PublishState: r.PublishState,
		PrNumber: r.PrNumber, PrURL: r.PrUrl, PrState: r.PrState, ChecksState: r.ChecksState, MergeSha: r.MergeSha,
		ReleaseState: r.ReleaseState, ReleaseRuns: json.RawMessage(r.ReleaseRuns),
	}
	if !json.Valid(v.ReleaseRuns) {
		v.ReleaseRuns = json.RawMessage("[]")
	}
	if r.MergedAt.Valid {
		t := r.MergedAt.Time
		v.MergedAt = &t
	}
	return v
}

type DocumentMeta struct {
	Kind      string    `json:"kind"`
	Version   int64     `json:"version"`
	Author    string    `json:"author"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
}

type DocumentView struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Version   int64           `json:"version"`
	Content   string          `json:"content"`
	Meta      json.RawMessage `json:"meta"`
	Author    string          `json:"author"`
	RunID     string          `json:"run_id"`
	CreatedAt time.Time       `json:"created_at"`
}

func documentView(d db.Document) DocumentView {
	meta := json.RawMessage(d.Meta)
	if !json.Valid(meta) {
		meta = json.RawMessage("{}")
	}
	return DocumentView{ID: d.ID, Kind: d.Kind, Version: d.Version, Content: d.Content, Meta: meta, Author: d.Author, RunID: d.RunID, CreatedAt: d.CreatedAt}
}

type RunView struct {
	ID             string          `json:"id"`
	UnitID         string          `json:"unit_id"`
	UnitLabel      string          `json:"unit_label,omitempty"`
	UnitTitle      string          `json:"unit_title,omitempty"`
	ProjectID      string          `json:"project_id"`
	ParentRunID    string          `json:"parent_run_id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason"`
	SessionID      string          `json:"session_id"`
	Model          string          `json:"model"`
	Effort         string          `json:"effort"`
	PermissionMode string          `json:"permission_mode"`
	PromptVersion  string          `json:"prompt_version"`
	Cwd            string          `json:"cwd"`
	CostUSD        float64         `json:"cost_usd"`
	CostTotalUSD   float64         `json:"cost_total_usd"`
	InputTokens    int64           `json:"input_tokens"`
	OutputTokens   int64           `json:"output_tokens"`
	Turns          int64           `json:"turns"`
	Denials        int64           `json:"denials"`
	Result         json.RawMessage `json:"result"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	EndedAt        *time.Time      `json:"ended_at"`
	DurationMS     int64           `json:"duration_ms"`
}

func runView(r db.Run) RunView {
	v := RunView{
		ID: r.ID, UnitID: r.UnitID.String, ProjectID: r.ProjectID.String, ParentRunID: r.ParentRunID, Kind: r.Kind,
		Status: r.Status, Reason: r.Reason, SessionID: r.SessionID, Model: r.Model, Effort: r.Effort,
		PermissionMode: r.PermissionMode, PromptVersion: r.PromptVersion, Cwd: r.Cwd, CostUSD: r.CostUsd,
		CostTotalUSD: r.CostTotalUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, Turns: r.Turns,
		Denials: r.Denials, Result: resultJSON(r.Result), CreatedAt: r.CreatedAt,
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time
		v.StartedAt = &t
		end := time.Now()
		if r.EndedAt.Valid {
			end = r.EndedAt.Time
		}
		v.DurationMS = end.Sub(t).Milliseconds()
	}
	if r.EndedAt.Valid {
		t := r.EndedAt.Time
		v.EndedAt = &t
	}
	return v
}

// resultJSON passes structured output through and wraps plain text.
func resultJSON(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

type RunEventView struct {
	Seq     int64           `json:"seq"`
	At      time.Time       `json:"at"`
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Summary string          `json:"summary,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

func runEventView(e db.RunEvent) RunEventView {
	return RunEventView{Seq: e.Seq, At: e.At, Type: e.Type, Subtype: e.Subtype, Tool: e.Tool, Summary: e.Summary, Payload: json.RawMessage(e.Payload)}
}

type RepoView struct {
	ID            string    `json:"id"`
	FullName      string    `json:"full_name"`
	DefaultBranch string    `json:"default_branch"`
	Cloned        bool      `json:"cloned"`
	CloneURL      string    `json:"clone_url"`
	CreatedAt     time.Time `json:"created_at"`
}

func repoView(r db.Repo) RepoView {
	return RepoView{ID: r.ID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, Cloned: r.ClonePath != "", CloneURL: r.CloneUrl, CreatedAt: r.CreatedAt}
}

type ProjectView struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Slug           string                 `json:"slug"`
	Description    string                 `json:"description"`
	ProductContext string                 `json:"product_context"`
	Settings       domain.ProjectSettings `json:"settings"`
	Repos          []RepoView             `json:"repos"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

func projectView(p db.Project, repos []db.Repo) ProjectView {
	v := ProjectView{
		ID: p.ID, Name: p.Name, Slug: p.Slug, Description: p.Description, ProductContext: p.ProductContext,
		Settings: domain.ParseProjectSettings(p.Settings), Repos: []RepoView{}, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	for _, r := range repos {
		v.Repos = append(v.Repos, repoView(r))
	}
	return v
}

type ActivityView struct {
	ID      int64           `json:"id"`
	UnitID  string          `json:"unit_id"`
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor"`
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func activityView(a db.Activity) ActivityView {
	data := json.RawMessage(a.Data)
	if !json.Valid(data) {
		data = json.RawMessage("{}")
	}
	return ActivityView{ID: a.ID, UnitID: a.UnitID.String, At: a.At, Actor: a.Actor, Kind: a.Kind, Message: a.Message, Data: data}
}

type SourceView struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"project_id"`
	ChannelID     string     `json:"channel_id"`
	ChannelName   string     `json:"channel_name"`
	AutoTriage    bool       `json:"auto_triage"`
	ExcludeBots   bool       `json:"exclude_bots"`
	PollIntervalS int64      `json:"poll_interval_s"`
	LastPolledAt  *time.Time `json:"last_polled_at"`
	LastError     string     `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
}

func sourceView(s db.SlackSource) SourceView {
	v := SourceView{
		ID: s.ID, ProjectID: s.ProjectID, ChannelID: s.ChannelID, ChannelName: s.ChannelName, AutoTriage: s.AutoTriage,
		ExcludeBots: s.ExcludeBots, PollIntervalS: s.PollIntervalS, LastError: s.LastError, CreatedAt: s.CreatedAt,
	}
	if s.LastPolledAt.Valid {
		t := s.LastPolledAt.Time
		v.LastPolledAt = &t
	}
	return v
}

type FeedbackView struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	ChannelID   string          `json:"channel_id"`
	ChannelName string          `json:"channel_name"`
	TS          string          `json:"ts"`
	ThreadTS    string          `json:"thread_ts"`
	Author      string          `json:"author"`
	Text        string          `json:"text"`
	Permalink   string          `json:"permalink"`
	ReplyCount  int64           `json:"reply_count"`
	Edited      bool            `json:"edited"`
	PostedAt    time.Time       `json:"posted_at"`
	Status      string          `json:"status"`
	Triage      json.RawMessage `json:"triage"`
	UnitID      string          `json:"unit_id"`
	UnitLabel   string          `json:"unit_label,omitempty"`
	UnitTitle   string          `json:"unit_title,omitempty"`
	UnitState   string          `json:"unit_state,omitempty"`
}

func feedbackView(f db.Feedback) FeedbackView {
	triage := json.RawMessage(f.Triage)
	if !json.Valid(triage) {
		triage = json.RawMessage("{}")
	}
	return FeedbackView{
		ID: f.ID, ProjectID: f.ProjectID, ChannelID: f.ChannelID, ChannelName: f.ChannelName, TS: f.Ts, ThreadTS: f.ThreadTs,
		Author: f.AuthorName, Text: f.Text, Permalink: f.Permalink, ReplyCount: f.ReplyCount, Edited: f.Edited,
		PostedAt: f.PostedAt, Status: f.TriageStatus, Triage: triage, UnitID: f.UnitID.String,
	}
}
