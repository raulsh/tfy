// Package api is thefactory's HTTP interface: a JSON API under /api/v1,
// server-sent event streams, and the embedded UI.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"github.com/raulsh/thefactory/internal/config"
	"github.com/raulsh/thefactory/internal/doctor"
	"github.com/raulsh/thefactory/internal/events"
	"github.com/raulsh/thefactory/internal/jobs"
	"github.com/raulsh/thefactory/internal/pipeline"
	"github.com/raulsh/thefactory/internal/store"
)

// TokenCookie carries the per-install token for the browser.
const TokenCookie = "thefactory_token"

// Options configure the server.
type Options struct {
	Pipeline *pipeline.Pipeline
	Store    *store.Store
	Hub      *events.Hub
	Jobs     *jobs.Queue
	Config   config.Config
	Paths    config.Paths
	Token    string
	Version  string
	// DevOrigins are extra browser origins allowed to call the API (the Vite
	// dev server).
	DevOrigins []string
	UI         fs.FS
	Log        *slog.Logger
}

// Server holds the handlers' dependencies.
type Server struct {
	Options
	allowedHosts   []string
	allowedOrigins []string
}

// New builds the Fiber app.
func New(o Options) *fiber.App {
	s := &Server{Options: o}
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		host := fmt.Sprintf("%s:%d", h, o.Config.Port)
		s.allowedHosts = append(s.allowedHosts, host)
		s.allowedOrigins = append(s.allowedOrigins, "http://"+host)
	}
	s.allowedOrigins = append(s.allowedOrigins, o.DevOrigins...)

	app := fiber.New(fiber.Config{
		AppName:      "thefactory",
		ErrorHandler: s.errorHandler,
		BodyLimit:    8 << 20,
		ReadTimeout:  30 * time.Second,
		// No write timeout: event streams stay open.
	})
	app.Use(recover.New())
	app.Use(s.hostGuard)

	v1 := app.Group("/api/v1", s.originGuard)
	v1.Post("/session", s.createSession)
	v1.Get("/health", s.health)
	v1.Use(s.requireToken)
	s.routes(v1)
	app.Use(s.ui())
	return app
}

// hostGuard rejects requests whose Host is not this machine: a DNS-rebinding
// page on another origin cannot reach the API through its own hostname.
func (s *Server) hostGuard(c fiber.Ctx) error {
	host := c.Host()
	if slices.Contains(s.allowedHosts, host) {
		return c.Next()
	}
	return fiber.NewError(fiber.StatusForbidden, "unexpected Host header")
}

// originGuard rejects state-changing requests from other browser origins.
func (s *Server) originGuard(c fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}
	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" || slices.Contains(s.allowedOrigins, origin) {
		return c.Next()
	}
	return fiber.NewError(fiber.StatusForbidden, "cross-origin request refused")
}

func (s *Server) tokenOK(c fiber.Ctx) bool {
	got := c.Cookies(TokenCookie)
	if got == "" {
		got = strings.TrimPrefix(c.Get(fiber.HeaderAuthorization), "Bearer ")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) requireToken(c fiber.Ctx) error {
	if s.tokenOK(c) {
		return c.Next()
	}
	return fiber.NewError(fiber.StatusUnauthorized, "open thefactory with the link `thefactory serve` printed")
}

// createSession trades the token from the printed link for a cookie.
func (s *Server) createSession(c fiber.Ctx) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := c.Bind().Body(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "send {\"token\": …}")
	}
	if subtle.ConstantTimeCompare([]byte(in.Token), []byte(s.Token)) != 1 {
		return fiber.NewError(fiber.StatusUnauthorized, "that token is not valid for this installation")
	}
	c.Cookie(&fiber.Cookie{
		Name:     TokenCookie,
		Value:    s.Token,
		Path:     "/",
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
	})
	return ok(c, map[string]bool{"authenticated": true})
}

func (s *Server) health(c fiber.Ctx) error {
	return ok(c, map[string]any{"ok": true, "version": s.Version, "authenticated": s.tokenOK(c)})
}

func (s *Server) doctor(c fiber.Ctx) error {
	return ok(c, doctor.Run(c.Context(), s.Config, s.Paths, doctor.Options{}))
}

// errorHandler renders every error in the ErrorResponse envelope, mapping
// pipeline errors to status codes.
func (s *Server) errorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	var fe *fiber.Error
	var conflict *pipeline.ConflictError
	var notFound *pipeline.NotFoundError
	var invalid *pipeline.InvalidError
	switch {
	case errors.As(err, &fe):
		code = fe.Code
	case errors.As(err, &conflict):
		code = fiber.StatusConflict
	case errors.As(err, &notFound), store.IsNotFound(err):
		code = fiber.StatusNotFound
		if store.IsNotFound(err) && notFound == nil {
			err = errors.New("not found")
		}
	case errors.As(err, &invalid):
		code = fiber.StatusBadRequest
	case errors.Is(err, context.Canceled):
		code = 499
	}
	if code >= 500 {
		s.Log.Error("request failed", "method", c.Method(), "path", c.Path(), "error", err)
	}
	return c.Status(code).JSON(ErrorResponse{Error: err.Error()})
}

// SuccessMessage and ErrorResponse are the response envelopes.
type SuccessMessage struct {
	Message string `json:"message,omitempty"`
	Data    any    `json:"data"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Details any    `json:"details,omitempty"`
}

func ok(c fiber.Ctx, data any) error {
	return c.JSON(SuccessMessage{Data: data})
}

func created(c fiber.Ctx, data any) error {
	return c.Status(fiber.StatusCreated).JSON(SuccessMessage{Data: data})
}

// ui serves the embedded single-page app, falling back to index.html for
// client-side routes.
func (s *Server) ui() fiber.Handler {
	index, err := fs.ReadFile(s.UI, "index.html")
	if err != nil {
		index = []byte(notBuiltPage)
	}
	return func(c fiber.Ctx) error {
		p := strings.TrimPrefix(c.Path(), "/")
		if strings.HasPrefix(p, "api/") {
			return fiber.NewError(fiber.StatusNotFound, "no such endpoint")
		}
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return fiber.NewError(fiber.StatusMethodNotAllowed, "method not allowed")
		}
		if clean, err := url.PathUnescape(p); err == nil && clean != "" && fs.ValidPath(clean) {
			if data, err := fs.ReadFile(s.UI, clean); err == nil {
				ext := clean[strings.LastIndexByte(clean, '.')+1:]
				c.Type(ext)
				if strings.HasPrefix(clean, "assets/") {
					c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
				}
				return c.Send(data)
			}
		}
		c.Set(fiber.HeaderCacheControl, "no-cache")
		c.Type("html")
		return c.Send(index)
	}
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>thefactory</title></head>
<body style="font-family: system-ui, sans-serif; padding: 2rem; color: #101828">
<h1>thefactory</h1>
<p>The UI is not built into this binary. Run <code>make build</code>, or <code>make dev</code> for the Vite dev server.</p>
</body></html>`
