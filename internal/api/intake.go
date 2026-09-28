package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/pipeline"
	"github.com/raulsh/tfy/internal/store/db"
)

func (s *Server) intakeRoutes(r fiber.Router) {
	r.Get("/pickers/slack-channels", s.slackChannels)
	r.Get("/projects/:id/sources", s.listSources)
	r.Post("/projects/:id/sources", s.addSource)
	r.Patch("/sources/:id", s.updateSource)
	r.Delete("/sources/:id", s.removeSource)
	r.Post("/sources/:id/poll", s.pollSource)
	r.Get("/feedback", s.listFeedback)
	r.Patch("/feedback/:id", s.patchFeedback)
	r.Get("/feedback/:id/thread", s.feedbackThread)
	r.Post("/feedback/import", s.importFeedback)
	r.Post("/feedback/units", s.unitFromFeedback)
}

func (s *Server) slackChannels(c fiber.Ctx) error {
	if s.Pipeline.Slack == nil {
		return &pipeline.ConflictError{Msg: "slk is not available: install it and run `slk configure`"}
	}
	convs, err := s.Pipeline.Slack.Conversations(c.Context(), c.Query("q"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return ok(c, convs)
}

func (s *Server) listSources(c fiber.Ctx) error {
	srcs, err := s.Store.Q.ListSlackSourcesByProject(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	out := make([]SourceView, 0, len(srcs))
	for _, src := range srcs {
		out = append(out, sourceView(src))
	}
	return ok(c, out)
}

func (s *Server) addSource(c fiber.Ctx) error {
	var in pipeline.SourceInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	src, err := s.Pipeline.AddSource(c.Context(), c.Params("id"), in)
	if err != nil {
		return err
	}
	return created(c, sourceView(src))
}

func (s *Server) updateSource(c fiber.Ctx) error {
	var in pipeline.SourceInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	src, err := s.Pipeline.UpdateSource(c.Context(), c.Params("id"), in)
	if err != nil {
		return err
	}
	return ok(c, sourceView(src))
}

func (s *Server) removeSource(c fiber.Ctx) error {
	if err := s.Pipeline.RemoveSource(c.Context(), c.Params("id")); err != nil {
		return err
	}
	return ok(c, map[string]bool{"deleted": true})
}

func (s *Server) pollSource(c fiber.Ctx) error {
	src, err := s.Store.Q.GetSlackSource(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	n, err := s.Pipeline.PollSource(c.Context(), src)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return ok(c, map[string]int{"new": n})
}

func (s *Server) listFeedback(c fiber.Ctx) error {
	rows, err := s.Store.Q.ListFeedback(c.Context(), db.ListFeedbackParams{ProjectID: optional(c.Query("project_id")), Lim: limit(c, 1000, 10000)})
	if err != nil {
		return err
	}
	out := make([]FeedbackView, 0, len(rows))
	for _, r := range rows {
		v := feedbackView(db.Feedback{
			ID: r.ID, ProjectID: r.ProjectID, SourceID: r.SourceID, ChannelID: r.ChannelID, ChannelName: r.ChannelName, Ts: r.Ts,
			ThreadTs: r.ThreadTs, AuthorID: r.AuthorID, AuthorName: r.AuthorName, Text: r.Text, Permalink: r.Permalink,
			ReplyCount: r.ReplyCount, Edited: r.Edited, PostedAt: r.PostedAt, TriageStatus: r.TriageStatus, Triage: r.Triage,
			UnitID: r.UnitID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if r.UnitSeq > 0 {
			v.UnitLabel, v.UnitTitle, v.UnitState = domain.Label(r.UnitSeq), r.UnitTitle, r.UnitState
		}
		out = append(out, v)
	}
	return ok(c, out)
}

func (s *Server) patchFeedback(c fiber.Ctx) error {
	var in struct {
		Status string `json:"status"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	f, err := s.Pipeline.SetFeedbackStatus(c.Context(), c.Params("id"), in.Status)
	if err != nil {
		return err
	}
	return ok(c, feedbackView(f))
}

func (s *Server) feedbackThread(c fiber.Ctx) error {
	f, err := s.Store.Q.GetFeedback(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	parentTS := f.Ts
	if f.ThreadTs != "" {
		parentTS = f.ThreadTs
	}
	out := []FeedbackView{}
	if parent, err := s.Store.Q.GetFeedbackByTS(c.Context(), db.GetFeedbackByTSParams{ChannelID: f.ChannelID, Ts: parentTS}); err == nil {
		out = append(out, feedbackView(parent))
	}
	replies, err := s.Store.Q.ListThreadReplies(c.Context(), db.ListThreadRepliesParams{ChannelID: f.ChannelID, ThreadTs: parentTS})
	if err != nil {
		return err
	}
	for _, r := range replies {
		out = append(out, feedbackView(r))
	}
	return ok(c, out)
}

func (s *Server) importFeedback(c fiber.Ctx) error {
	var in struct {
		ProjectID  string   `json:"project_id"`
		Permalinks []string `json:"permalinks"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	fbs, err := s.Pipeline.ImportPermalinks(c.Context(), in.ProjectID, in.Permalinks)
	if err != nil {
		return err
	}
	out := make([]FeedbackView, 0, len(fbs))
	for _, f := range fbs {
		out = append(out, feedbackView(f))
	}
	return ok(c, out)
}

func (s *Server) unitFromFeedback(c fiber.Ctx) error {
	var in pipeline.FromFeedbackInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	u, err := s.Pipeline.CreateUnitFromFeedback(c.Context(), in)
	if err != nil {
		return err
	}
	busy := s.Pipeline.Busy(c.Context(), u.ID)
	return created(c, unitView(u, busy, s.Pipeline.Actions(c.Context(), u, busy), ""))
}
