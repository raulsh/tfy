package api

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/pipeline"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

func (s *Server) routes(r fiber.Router) {
	r.Get("/doctor", s.doctor)
	r.Get("/config", s.configView)
	r.Get("/stats", s.stats)
	r.Get("/stats/daily", s.dailyStats)
	r.Get("/activity", s.recentActivity)

	r.Get("/projects", s.listProjects)
	r.Post("/projects", s.createProject)
	r.Get("/projects/:id", s.getProject)
	r.Patch("/projects/:id", s.updateProject)
	r.Delete("/projects/:id", s.deleteProject)
	r.Post("/projects/:id/repos", s.linkRepo)
	r.Delete("/projects/:id/repos/:repoID", s.unlinkRepo)
	r.Get("/projects/:id/conventions", s.projectConventions)

	r.Get("/pickers/github-owners", s.githubOwners)
	r.Get("/pickers/github-repos", s.githubRepos)

	r.Get("/units", s.listUnits)
	r.Post("/units", s.createUnit)
	r.Get("/units/:id", s.getUnit)
	r.Post("/units/:id/actions/:action", s.unitAction)
	r.Get("/units/:id/documents/:kind", s.getDocument)
	r.Put("/units/:id/documents/:kind", s.putDocument)

	r.Get("/runs", s.listRuns)
	r.Get("/runs/:id", s.getRun)
	r.Get("/runs/:id/events", s.runEvents)
	r.Get("/runs/:id/stream", s.runStream())
	r.Post("/runs/:id/cancel", s.cancelRun)

	r.Get("/events", s.globalStream())
	s.intakeRoutes(r)
	s.issueRoutes(r)
}

func limit(c fiber.Ctx, def, max int) int64 {
	n, err := strconv.Atoi(c.Query("limit"))
	if err != nil || n <= 0 {
		n = def
	}
	return int64(min(n, max))
}

func optional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- configuration and health ---

func (s *Server) configView(c fiber.Ctx) error {
	type stageView struct {
		Model   string  `json:"model"`
		Effort  string  `json:"effort"`
		Budget  float64 `json:"budget_usd"`
		Timeout string  `json:"timeout"`
	}
	stages := map[string]stageView{}
	for _, kind := range []string{"triage", "define", "plan", "develop", "review", "release", "learn", "issue"} {
		st := s.Config.Stage(kind)
		stages[kind] = stageView{st.Model, st.Effort, st.Budget, st.Timeout.String()}
	}
	return ok(c, map[string]any{
		"host":                s.Config.Host,
		"port":                s.Config.Port,
		"max_concurrent_runs": s.Config.MaxConcurrentRuns,
		"stages":              stages,
		"pr_poll_interval":    s.Config.PRPollInterval.String(),
		"slack_poll_interval": s.Config.SlackPollInterval.String(),
		"data_dir":            s.Paths.Root,
		"config_file":         s.Paths.Config(),
		"version":             s.Version,
	})
}

func (s *Server) stats(c fiber.Ctx) error {
	ctx := c.Context()
	units, err := s.Store.Q.ListUnits(ctx, db.ListUnitsParams{ProjectID: optional(c.Query("project_id")), Lim: 10000})
	if err != nil {
		return err
	}
	byStage := map[string]int{}
	attention, waiting := 0, 0
	for _, u := range units {
		state := domain.State(u.State)
		byStage[string(state.Stage())]++
		if u.Attention != "" {
			attention++
		}
		if !state.Terminal() && (u.Attention != "" || state.WaitsOnHuman()) {
			waiting++
		}
	}
	live, err := s.Store.Q.ListLiveRuns(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC()
	costToday, _ := s.Store.Q.SumCost(ctx, today)
	cost7d, _ := s.Store.Q.SumCost(ctx, today.AddDate(0, 0, -6))
	inbox := map[string]int64{}
	if counts, err := s.Store.Q.CountFeedbackByStatus(ctx, optional(c.Query("project_id"))); err == nil {
		for _, row := range counts {
			inbox[row.TriageStatus] = row.N
		}
	}
	out := map[string]any{
		"feedback_by_status": inbox,
		"slack_enabled":      s.Pipeline.Slack != nil,
		"units_by_stage":     byStage,
		"units_total":        len(units),
		"needs_attention":    attention,
		"waiting_on_you":     waiting,
		"live_runs":          len(live),
		"cost_today_usd":     costToday,
		"cost_7d_usd":        cost7d,
		"paused_until":       nil,
		"quota":              s.Pipeline.Quota(),
	}
	if t := s.Jobs.PausedUntil(); !t.IsZero() {
		out["paused_until"] = t
	}
	return ok(c, out)
}

// --- projects ---

func (s *Server) projectView(c fiber.Ctx, p db.Project) (ProjectView, error) {
	repos, err := s.Store.Q.ListReposByProject(c.Context(), p.ID)
	return projectView(p, repos), err
}

func (s *Server) listProjects(c fiber.Ctx) error {
	ps, err := s.Store.Q.ListProjects(c.Context())
	if err != nil {
		return err
	}
	out := make([]ProjectView, 0, len(ps))
	for _, p := range ps {
		v, err := s.projectView(c, p)
		if err != nil {
			return err
		}
		out = append(out, v)
	}
	return ok(c, out)
}

func (s *Server) createProject(c fiber.Ctx) error {
	var in pipeline.ProjectInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	p, err := s.Pipeline.CreateProject(c.Context(), in)
	if err != nil {
		return err
	}
	v, err := s.projectView(c, p)
	if err != nil {
		return err
	}
	return created(c, v)
}

func (s *Server) getProject(c fiber.Ctx) error {
	p, err := s.Store.Q.GetProject(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	v, err := s.projectView(c, p)
	if err != nil {
		return err
	}
	return ok(c, v)
}

func (s *Server) updateProject(c fiber.Ctx) error {
	var in pipeline.ProjectInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	p, err := s.Pipeline.UpdateProject(c.Context(), c.Params("id"), in)
	if err != nil {
		return err
	}
	v, err := s.projectView(c, p)
	if err != nil {
		return err
	}
	return ok(c, v)
}

// projectConventions shows what Claude Code follows in each of the
// project's repositories, from their default branches; ?refresh=1 fetches
// first.
func (s *Server) projectConventions(c fiber.Ctx) error {
	out, err := s.Pipeline.Conventions(c.Context(), c.Params("id"), c.Query("refresh") == "1")
	if err != nil {
		return err
	}
	return ok(c, out)
}

func (s *Server) deleteProject(c fiber.Ctx) error {
	if err := s.Pipeline.DeleteProject(c.Context(), c.Params("id")); err != nil {
		return err
	}
	return ok(c, map[string]bool{"deleted": true})
}

func (s *Server) linkRepo(c fiber.Ctx) error {
	var in struct {
		FullName string `json:"full_name"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	r, err := s.Pipeline.LinkRepo(c.Context(), c.Params("id"), in.FullName)
	if err != nil {
		return err
	}
	return created(c, repoView(r))
}

func (s *Server) unlinkRepo(c fiber.Ctx) error {
	if err := s.Pipeline.UnlinkRepo(c.Context(), c.Params("id"), c.Params("repoID")); err != nil {
		return err
	}
	return ok(c, map[string]bool{"deleted": true})
}

func (s *Server) githubOwners(c fiber.Ctx) error {
	ctx := c.Context()
	user, err := s.Pipeline.GH.User(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	orgs, _ := s.Pipeline.GH.Orgs(ctx)
	return ok(c, append([]string{user}, orgs...))
}

func (s *Server) githubRepos(c fiber.Ctx) error {
	repos, err := s.Pipeline.GH.RepoList(c.Context(), c.Query("owner"), 300)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	type item struct {
		FullName      string    `json:"full_name"`
		Description   string    `json:"description"`
		Private       bool      `json:"private"`
		DefaultBranch string    `json:"default_branch"`
		UpdatedAt     time.Time `json:"updated_at"`
	}
	out := make([]item, 0, len(repos))
	for _, r := range repos {
		out = append(out, item{r.NameWithOwner, r.Description, r.IsPrivate, r.DefaultBranchRef.Name, r.UpdatedAt})
	}
	return ok(c, out)
}

// --- units ---

func (s *Server) projectNames(c fiber.Ctx) (map[string]string, error) {
	ps, err := s.Store.Q.ListProjects(c.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(ps))
	for _, p := range ps {
		names[p.ID] = p.Name
	}
	return names, nil
}

func (s *Server) listUnits(c fiber.Ctx) error {
	ctx := c.Context()
	units, err := s.Store.Q.ListUnits(ctx, db.ListUnitsParams{ProjectID: optional(c.Query("project_id")), Lim: limit(c, 500, 5000)})
	if err != nil {
		return err
	}
	busyIDs, err := s.Store.Q.ListBusyUnitIDs(ctx)
	if err != nil {
		return err
	}
	names, err := s.projectNames(c)
	if err != nil {
		return err
	}
	out := make([]UnitView, 0, len(units))
	for _, u := range units {
		busy := slices.Contains(busyIDs, u.ID)
		out = append(out, unitView(u, busy, pipeline.AvailableActions(u, busy), names[u.ProjectID]))
	}
	return ok(c, out)
}

func (s *Server) createUnit(c fiber.Ctx) error {
	var in pipeline.CreateUnitInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	u, err := s.Pipeline.CreateUnit(c.Context(), in)
	if err != nil {
		return err
	}
	busy := s.Pipeline.Busy(c.Context(), u.ID)
	return created(c, unitView(u, busy, pipeline.AvailableActions(u, busy), ""))
}

// UnitDetail is everything the unit page shows.
type UnitDetail struct {
	UnitView
	Repos     []UnitRepoView          `json:"repos"`
	Documents map[string]DocumentMeta `json:"documents"`
	Runs      []RunView               `json:"runs"`
	Activity  []ActivityView          `json:"activity"`
	Feedback  []FeedbackView          `json:"feedback"`
	Issues    []IssueView             `json:"issues"`
	MergePlan pipeline.MergePlanView  `json:"merge_plan"`
}

func (s *Server) getUnit(c fiber.Ctx) error {
	ctx := c.Context()
	u, err := s.Store.Q.GetUnit(ctx, c.Params("id"))
	if err != nil {
		return err
	}
	project, _ := s.Store.Q.GetProject(ctx, u.ProjectID)
	busy := s.Pipeline.Busy(ctx, u.ID)
	d := UnitDetail{
		UnitView:  unitView(u, busy, pipeline.AvailableActions(u, busy), project.Name),
		Repos:     []UnitRepoView{},
		Documents: map[string]DocumentMeta{},
		Runs:      []RunView{},
		Activity:  []ActivityView{},
		Feedback:  []FeedbackView{},
	}
	if fbs, err := s.Store.Q.ListFeedbackByUnit(ctx, store.NullString(u.ID)); err == nil {
		for _, f := range fbs {
			d.Feedback = append(d.Feedback, feedbackView(f))
		}
	}
	urs, err := s.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, r := range urs {
		d.Repos = append(d.Repos, unitRepoView(r))
	}
	for _, kind := range pipeline.DocKinds {
		if doc, err := s.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: kind}); err == nil {
			d.Documents[kind] = DocumentMeta{Kind: kind, Version: doc.Version, Author: doc.Author, RunID: doc.RunID, CreatedAt: doc.CreatedAt}
		}
	}
	runs, err := s.Store.Q.ListRuns(ctx, db.ListRunsParams{UnitID: u.ID, ProjectID: nil, Lim: 100})
	if err != nil {
		return err
	}
	for _, r := range runs {
		d.Runs = append(d.Runs, runRowView(r))
	}
	acts, err := s.Store.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 200})
	if err != nil {
		return err
	}
	for _, a := range acts {
		d.Activity = append(d.Activity, activityView(a))
	}
	if d.Issues, err = s.unitIssueViews(c, u.ID); err != nil {
		return err
	}
	if d.MergePlan, err = s.Pipeline.MergePlan(ctx, u); err != nil {
		return err
	}
	return ok(c, d)
}

func (s *Server) unitAction(c fiber.Ctx) error {
	var in pipeline.ActionInput
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
	}
	u, err := s.Pipeline.Act(c.Context(), c.Params("id"), c.Params("action"), in)
	if err != nil {
		return err
	}
	busy := s.Pipeline.Busy(c.Context(), u.ID)
	return ok(c, unitView(u, busy, pipeline.AvailableActions(u, busy), ""))
}

func (s *Server) getDocument(c fiber.Ctx) error {
	ctx := c.Context()
	unitID, kind := c.Params("id"), c.Params("kind")
	var doc db.Document
	var err error
	if v, perr := strconv.Atoi(c.Query("version")); perr == nil && v > 0 {
		doc, err = s.Store.Q.GetDocumentVersion(ctx, db.GetDocumentVersionParams{UnitID: unitID, Kind: kind, Version: int64(v)})
	} else {
		doc, err = s.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: unitID, Kind: kind})
	}
	if err != nil {
		return err
	}
	versions, err := s.Store.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: unitID, Kind: kind})
	if err != nil {
		return err
	}
	metas := make([]DocumentMeta, 0, len(versions))
	for _, v := range versions {
		metas = append(metas, DocumentMeta{Kind: v.Kind, Version: v.Version, Author: v.Author, RunID: v.RunID, CreatedAt: v.CreatedAt})
	}
	return ok(c, map[string]any{"document": documentView(doc), "versions": metas})
}

func (s *Server) putDocument(c fiber.Ctx) error {
	var in struct {
		Content string `json:"content"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	doc, err := s.Pipeline.SaveDocument(c.Context(), c.Params("id"), c.Params("kind"), in.Content, "user")
	if err != nil {
		return err
	}
	return ok(c, documentView(doc))
}

// --- runs ---

func runRowView(r db.ListRunsRow) RunView {
	v := runView(db.Run{
		ID: r.ID, UnitID: r.UnitID, ProjectID: r.ProjectID, ParentRunID: r.ParentRunID, Kind: r.Kind, Status: r.Status,
		Reason: r.Reason, SessionID: r.SessionID, Model: r.Model, Effort: r.Effort, PermissionMode: r.PermissionMode,
		PromptVersion: r.PromptVersion, Cwd: r.Cwd, Pid: r.Pid, CostUsd: r.CostUsd, CostTotalUsd: r.CostTotalUsd,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, Turns: r.Turns, Denials: r.Denials, Result: r.Result,
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
	})
	if r.UnitSeq > 0 {
		v.UnitLabel = domain.Label(r.UnitSeq)
	}
	v.UnitTitle = r.UnitTitle
	return v
}

func (s *Server) listRuns(c fiber.Ctx) error {
	runs, err := s.Store.Q.ListRuns(c.Context(), db.ListRunsParams{
		UnitID: optional(c.Query("unit_id")), ProjectID: optional(c.Query("project_id")), Lim: limit(c, 200, 2000),
	})
	if err != nil {
		return err
	}
	out := make([]RunView, 0, len(runs))
	for _, r := range runs {
		out = append(out, runRowView(r))
	}
	return ok(c, out)
}

func (s *Server) getRun(c fiber.Ctx) error {
	r, err := s.Store.Q.GetRun(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	v := runView(r)
	if r.UnitID.Valid {
		if u, err := s.Store.Q.GetUnit(c.Context(), r.UnitID.String); err == nil {
			v.UnitLabel, v.UnitTitle = domain.Label(u.Seq), u.Title
		}
	}
	return ok(c, v)
}

func (s *Server) runEvents(c fiber.Ctx) error {
	after, _ := strconv.ParseInt(c.Query("after"), 10, 64)
	evs, err := s.Store.Q.ListRunEvents(c.Context(), db.ListRunEventsParams{RunID: c.Params("id"), After: after, Lim: limit(c, 2000, 10000)})
	if err != nil {
		return err
	}
	out := make([]RunEventView, 0, len(evs))
	for _, e := range evs {
		out = append(out, runEventView(e))
	}
	return ok(c, out)
}

func (s *Server) cancelRun(c fiber.Ctx) error {
	r, err := s.Store.Q.GetRun(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	if r.Status != "running" && r.Status != "queued" {
		return &pipeline.ConflictError{Msg: "the run is not running"}
	}
	if !r.UnitID.Valid {
		return &pipeline.ConflictError{Msg: "the run belongs to no unit"}
	}
	if _, err := s.Pipeline.Act(c.Context(), r.UnitID.String, pipeline.ActionCancel, pipeline.ActionInput{}); err != nil {
		return err
	}
	return ok(c, map[string]bool{"cancelled": true})
}

func (s *Server) dailyStats(c fiber.Ctx) error {
	ctx := c.Context()
	days, _ := strconv.Atoi(c.Query("days"))
	if days <= 0 || days > 90 {
		days = 14
	}
	since := time.Now().AddDate(0, 0, -days+1).Truncate(24 * time.Hour).UTC()
	cost, err := s.Store.Q.DailyCost(ctx, since)
	if err != nil {
		return err
	}
	done, err := s.Store.Q.DailyDone(ctx, since)
	if err != nil {
		return err
	}
	type day struct {
		Day  string  `json:"day"`
		Cost float64 `json:"cost_usd"`
		Runs int64   `json:"runs"`
		Done int64   `json:"done"`
	}
	byDay := map[string]*day{}
	var out []*day
	for i := range days {
		d := since.AddDate(0, 0, i).Format("2006-01-02")
		byDay[d] = &day{Day: d}
		out = append(out, byDay[d])
	}
	for _, r := range cost {
		if d, ok := byDay[r.Day]; ok {
			d.Cost, d.Runs = r.Cost, r.Runs
		}
	}
	for _, r := range done {
		if d, ok := byDay[r.Day]; ok {
			d.Done = r.N
		}
	}
	return ok(c, out)
}

func (s *Server) recentActivity(c fiber.Ctx) error {
	ctx := c.Context()
	acts, err := s.Store.Q.ListRecentActivity(ctx, limit(c, 50, 500))
	if err != nil {
		return err
	}
	labels := map[string]string{}
	type item struct {
		ActivityView
		UnitLabel string `json:"unit_label"`
		UnitTitle string `json:"unit_title"`
	}
	out := make([]item, 0, len(acts))
	for _, a := range acts {
		it := item{ActivityView: activityView(a)}
		if a.UnitID.Valid {
			if _, seen := labels[a.UnitID.String]; !seen {
				if u, err := s.Store.Q.GetUnit(ctx, a.UnitID.String); err == nil {
					labels[a.UnitID.String] = domain.Label(u.Seq) + "\x00" + u.Title
				} else {
					labels[a.UnitID.String] = ""
				}
			}
			if l := labels[a.UnitID.String]; l != "" {
				parts := strings.SplitN(l, "\x00", 2)
				it.UnitLabel, it.UnitTitle = parts[0], parts[1]
			}
		}
		out = append(out, it)
	}
	return ok(c, out)
}
