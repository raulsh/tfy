package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/raulsh/tfy/internal/pipeline"
	"github.com/raulsh/tfy/internal/store/db"
)

func (s *Server) issueRoutes(r fiber.Router) {
	r.Get("/projects/:id/issues", s.searchIssues)
	r.Post("/units/:id/issues", s.linkIssue)
	r.Patch("/units/:id/issues/:issueID", s.updateIssueLink)
	r.Delete("/units/:id/issues/:issueID", s.unlinkIssue)
	r.Post("/units/:id/issues/:issueID/refresh", s.refreshIssue)
	r.Post("/units/:id/issues/:issueID/check", s.checkIssue)
	r.Post("/units/:id/issues/:issueID/apply", s.applyIssue)
	r.Post("/units/:id/issues/:issueID/dismiss", s.dismissIssue)
}

// IssueView is a GitHub issue linked to a unit, as tfy last read it.
type IssueView struct {
	ID              string                    `json:"id"`
	Repo            string                    `json:"repo"`
	Number          int64                     `json:"number"`
	Ref             string                    `json:"ref"`
	URL             string                    `json:"url"`
	Title           string                    `json:"title"`
	State           string                    `json:"state"`
	Author          string                    `json:"author"`
	Labels          []string                  `json:"labels"`
	Body            string                    `json:"body"`
	Comments        []pipeline.IssueComment   `json:"comments"`
	Public          bool                      `json:"public"`
	Closes          bool                      `json:"closes"`
	FetchedAt       *time.Time                `json:"fetched_at"`
	IssueUpdatedAt  *time.Time                `json:"issue_updated_at"`
	SuggestionState string                    `json:"suggestion_state"`
	Suggestion      *pipeline.IssueSuggestion `json:"suggestion,omitempty"`
}

// issueView presents a linked issue. checking says whether its check is
// still queued or running: a "running" state without one was interrupted.
func issueView(is db.UnitIssue, checking bool) IssueView {
	v := IssueView{
		ID: is.ID, Repo: is.Repo, Number: is.Number, Ref: fmt.Sprintf("%s#%d", is.Repo, is.Number), URL: is.Url, Title: is.Title,
		State: is.State, Author: is.Author, Labels: []string{}, Body: is.Body, Comments: []pipeline.IssueComment{},
		Public: is.Public, Closes: is.Closes, SuggestionState: is.SuggestionState,
	}
	_ = json.Unmarshal([]byte(is.Labels), &v.Labels)
	_ = json.Unmarshal([]byte(is.Comments), &v.Comments)
	if is.FetchedAt.Valid {
		t := is.FetchedAt.Time
		v.FetchedAt = &t
	}
	if is.IssueUpdatedAt.Valid {
		t := is.IssueUpdatedAt.Time
		v.IssueUpdatedAt = &t
	}
	if is.Suggestion != "" {
		var sug pipeline.IssueSuggestion
		if json.Unmarshal([]byte(is.Suggestion), &sug) == nil {
			v.Suggestion = &sug
		}
	}
	if v.SuggestionState == pipeline.SuggestionRunning && !checking {
		v.SuggestionState = pipeline.SuggestionFailed
		if v.Suggestion == nil {
			v.Suggestion = &pipeline.IssueSuggestion{}
		}
		v.Suggestion.Error = "the check was interrupted; run it again"
	}
	return v
}

func (s *Server) unitIssueViews(c fiber.Ctx, unitID string) ([]IssueView, error) {
	rows, err := s.Store.Q.ListUnitIssues(c.Context(), unitID)
	if err != nil {
		return nil, err
	}
	out := make([]IssueView, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueView(r, s.Pipeline.IssueCheckActive(c.Context(), r.ID)))
	}
	return out, nil
}

func (s *Server) issueResponse(c fiber.Ctx, row db.UnitIssue, err error) error {
	if err != nil {
		return err
	}
	return ok(c, issueView(row, s.Pipeline.IssueCheckActive(c.Context(), row.ID)))
}

func (s *Server) searchIssues(c fiber.Ctx) error {
	out, err := s.Pipeline.SearchIssues(c.Context(), c.Params("id"), c.Query("q"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	return ok(c, out)
}

func (s *Server) linkIssue(c fiber.Ctx) error {
	var in struct {
		Ref    string `json:"ref"`
		Closes *bool  `json:"closes"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	closes := in.Closes == nil || *in.Closes
	row, err := s.Pipeline.LinkIssue(c.Context(), c.Params("id"), in.Ref, closes, "")
	if err != nil {
		return err
	}
	return created(c, issueView(row, false))
}

func (s *Server) updateIssueLink(c fiber.Ctx) error {
	var in struct {
		Closes bool `json:"closes"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	row, err := s.Pipeline.SetIssueCloses(c.Context(), c.Params("id"), c.Params("issueID"), in.Closes, "")
	return s.issueResponse(c, row, err)
}

func (s *Server) unlinkIssue(c fiber.Ctx) error {
	if err := s.Pipeline.UnlinkIssue(c.Context(), c.Params("id"), c.Params("issueID"), ""); err != nil {
		return err
	}
	return ok(c, map[string]bool{"unlinked": true})
}

func (s *Server) refreshIssue(c fiber.Ctx) error {
	row, err := s.Pipeline.RefreshIssue(c.Context(), c.Params("id"), c.Params("issueID"))
	return s.issueResponse(c, row, err)
}

func (s *Server) checkIssue(c fiber.Ctx) error {
	row, err := s.Pipeline.CheckIssue(c.Context(), c.Params("id"), c.Params("issueID"), "")
	return s.issueResponse(c, row, err)
}

func (s *Server) applyIssue(c fiber.Ctx) error {
	var in pipeline.IssueApplyInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	row, err := s.Pipeline.ApplyIssueSuggestion(c.Context(), c.Params("id"), c.Params("issueID"), in)
	return s.issueResponse(c, row, err)
}

func (s *Server) dismissIssue(c fiber.Ctx) error {
	row, err := s.Pipeline.DismissIssueSuggestion(c.Context(), c.Params("id"), c.Params("issueID"), "")
	return s.issueResponse(c, row, err)
}
