package api

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"

	"github.com/raulsh/tfy/internal/config"
)

func testApp(host string) *fiber.App {
	cfg := config.Default()
	cfg.Host = host
	return New(Options{Config: cfg, Token: strings.Repeat("a", 48), UI: fstest.MapFS{}})
}

func status(t *testing.T, app *fiber.App, method, host, origin string) int {
	t.Helper()
	path := "/api/v1/health"
	if method == fiber.MethodPost {
		path = "/api/v1/session"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(`{"token": "wrong"}`))
	req.Host = host
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	if origin != "" {
		req.Header.Set(fiber.HeaderOrigin, origin)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestLoopbackServerRefusesNetworkHosts(t *testing.T) {
	app := testApp("127.0.0.1")
	for _, host := range []string{"127.0.0.1:7420", "localhost:7420", "[::1]:7420"} {
		if got := status(t, app, fiber.MethodGet, host, ""); got != fiber.StatusOK {
			t.Errorf("Host %s: status %d, want 200", host, got)
		}
	}
	name, _ := os.Hostname()
	for _, host := range []string{"192.168.1.10:7420", name + ":7420", "evil.example:7420", "127.0.0.1:80"} {
		if got := status(t, app, fiber.MethodGet, host, ""); got != fiber.StatusForbidden {
			t.Errorf("Host %s: status %d, want 403", host, got)
		}
	}
}

func TestNetworkServerAdmitsAddressesButNotOtherNames(t *testing.T) {
	app := testApp("0.0.0.0")
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1:7420", "192.168.1.10:7420", "[fd00::10]:7420", strings.ToLower(name) + ":7420", name + ".local:7420"} {
		if got := status(t, app, fiber.MethodGet, host, ""); got != fiber.StatusOK {
			t.Errorf("Host %s: status %d, want 200", host, got)
		}
	}
	// A DNS-rebinding page reaches the server under its own name.
	for _, host := range []string{"evil.example:7420", "192.168.1.10:80", "192.168.1.10"} {
		if got := status(t, app, fiber.MethodGet, host, ""); got != fiber.StatusForbidden {
			t.Errorf("Host %s: status %d, want 403", host, got)
		}
	}
}

func TestNetworkServerAdmitsOnlyItsOwnOrigin(t *testing.T) {
	app := testApp("0.0.0.0")
	// The wrong token gets past the origin guard to a 401.
	if got := status(t, app, fiber.MethodPost, "192.168.1.10:7420", "http://192.168.1.10:7420"); got != fiber.StatusUnauthorized {
		t.Errorf("same origin: status %d, want 401", got)
	}
	for _, origin := range []string{"http://evil.example", "http://192.168.1.99:7420", "https://192.168.1.10:7420"} {
		if got := status(t, app, fiber.MethodPost, "192.168.1.10:7420", origin); got != fiber.StatusForbidden {
			t.Errorf("Origin %s: status %d, want 403", origin, got)
		}
	}
}
