package api

import (
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/pipeline"
	"github.com/raulsh/tfy/internal/store/db"
)

func (s *Server) accountRoutes(r fiber.Router) {
	r.Get("/accounts", s.listAccounts)
	r.Post("/accounts", s.createAccount)
	r.Patch("/accounts/:id", s.updateAccount)
	r.Delete("/accounts/:id", s.deleteAccount)
	r.Put("/accounts/:id/secret", s.putAccountSecret)
	r.Post("/accounts/:id/test", s.testAccount)
	r.Post("/accounts/:id/resume", s.resumeAccount)
	r.Get("/settings/runs", s.getRunSettings)
	r.Put("/settings/runs", s.putRunSettings)
	r.Get("/engines", s.engines)
}

// AccountView is an account as the API shows it: whether it has a secret,
// never the secret.
type AccountView struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	CLI         string                 `json:"cli"`
	Kind        string                 `json:"kind"`
	Settings    domain.AccountSettings `json:"settings"`
	HasSecret   bool                   `json:"has_secret"`
	PausedUntil *time.Time             `json:"paused_until"`
	PauseReason string                 `json:"pause_reason,omitempty"`
	// ModelBlocks are the models it cannot run for now, by family: until
	// when, and why.
	ModelBlocks map[string]domain.ModelBlock `json:"model_blocks"`
	Quota       json.RawMessage              `json:"quota"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}

func (s *Server) accountView(a db.Account) AccountView {
	v := AccountView{
		ID: a.ID, Name: a.Name, CLI: a.Cli, Kind: a.Kind, Settings: domain.ParseAccountSettings(a.Settings),
		HasSecret: s.Pipeline.HasSecret(a.ID), Quota: json.RawMessage("null"), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if a.PausedUntil.Valid && a.PausedUntil.Time.After(time.Now()) {
		t := a.PausedUntil.Time
		v.PausedUntil, v.PauseReason = &t, a.PauseReason
	}
	v.ModelBlocks = domain.ParseModelBlocks(a.ModelBlocks, time.Now())
	if json.Valid([]byte(a.Quota)) {
		v.Quota = json.RawMessage(a.Quota)
	}
	return v
}

func (s *Server) accountViews(c fiber.Ctx) ([]AccountView, error) {
	rows, err := s.Store.Q.ListAccounts(c.Context())
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, 0, len(rows))
	for _, a := range rows {
		out = append(out, s.accountView(a))
	}
	return out, nil
}

func (s *Server) listAccounts(c fiber.Ctx) error {
	views, err := s.accountViews(c)
	if err != nil {
		return err
	}
	return ok(c, views)
}

func (s *Server) createAccount(c fiber.Ctx) error {
	var in pipeline.AccountInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	a, err := s.Pipeline.CreateAccount(c.Context(), in)
	if err != nil {
		return err
	}
	return created(c, s.accountView(a))
}

func (s *Server) updateAccount(c fiber.Ctx) error {
	var in pipeline.AccountInput
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	a, err := s.Pipeline.UpdateAccount(c.Context(), c.Params("id"), in)
	if err != nil {
		return err
	}
	return ok(c, s.accountView(a))
}

func (s *Server) deleteAccount(c fiber.Ctx) error {
	if err := s.Pipeline.DeleteAccount(c.Context(), c.Params("id")); err != nil {
		return err
	}
	return ok(c, map[string]bool{"deleted": true})
}

// putAccountSecret replaces an account's secret; an empty one removes it.
func (s *Server) putAccountSecret(c fiber.Ctx) error {
	var in struct {
		Secret string `json:"secret"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if err := s.Pipeline.SetAccountSecret(c.Context(), c.Params("id"), in.Secret); err != nil {
		return err
	}
	return ok(c, map[string]bool{"saved": true})
}

// testAccount reports how an account signs in; ?probe=1 also sends it a
// one-word prompt.
func (s *Server) testAccount(c fiber.Ctx) error {
	t, err := s.Pipeline.TestAccount(c.Context(), c.Params("id"), c.Query("probe") == "1")
	if err != nil {
		return err
	}
	return ok(c, t)
}

func (s *Server) resumeAccount(c fiber.Ctx) error {
	if err := s.Pipeline.ResumeAccount(c.Context(), c.Params("id")); err != nil {
		return err
	}
	return ok(c, map[string]bool{"resumed": true})
}

func (s *Server) getRunSettings(c fiber.Ctx) error {
	return ok(c, s.Pipeline.GlobalRunSettings(c.Context()))
}

func (s *Server) putRunSettings(c fiber.Ctx) error {
	var in domain.RunSettings
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	out, err := s.Pipeline.SetGlobalRunSettings(c.Context(), in)
	if err != nil {
		return err
	}
	return ok(c, out)
}

// engines lists the coding CLIs: how their accounts sign in, the stages
// they may run, and their models and effort levels.
func (s *Server) engines(c fiber.Ctx) error {
	out := make([]domain.CLI, 0, len(domain.CLIs))
	for _, name := range slices.Sorted(maps.Keys(domain.CLIs)) {
		out = append(out, domain.CLIs[name])
	}
	return ok(c, out)
}
