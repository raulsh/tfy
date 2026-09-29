package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// An artifact is written by an agent, so whether it is framed or opened in
// a tab of its own, it gets an opaque origin: no cookie, no tfy API, and no
// network to send anything to.
func TestArtifactsAreConfined(t *testing.T) {
	app := fiber.New()
	app.Get("/a", func(c fiber.Ctx) error {
		return sendArtifact(c, "mocks/health page.html", "text/html; charset=utf-8", []byte("<script>fetch('/api/v1/units')</script>"))
	})
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/a", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	csp := resp.Header.Get(fiber.HeaderContentSecurityPolicy)
	for _, want := range []string{"sandbox allow-scripts;", "default-src 'none'", "form-action 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
	for _, never := range []string{"allow-same-origin", "allow-top-navigation", "allow-popups", "allow-forms", "https:", "connect-src"} {
		if strings.Contains(csp, never) {
			t.Errorf("CSP %q must not have %q", csp, never)
		}
	}
	if got := resp.Header.Get(fiber.HeaderXContentTypeOptions); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := resp.Header.Get(fiber.HeaderContentType); got != "text/html; charset=utf-8" || !strings.HasPrefix(string(body), "<script>") {
		t.Errorf("served %q as %q", body, got)
	}
	if got := resp.Header.Get(fiber.HeaderContentDisposition); got != `inline; filename="health page.html"` {
		t.Errorf("Content-Disposition = %q", got)
	}
}
