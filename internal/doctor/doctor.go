// Package doctor checks that thefactory's dependencies are installed and
// signed in.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/raulsh/thefactory/internal/config"
)

// Check statuses.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
)

// Check is one dependency's health.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Options select what to check.
type Options struct {
	// CheckPort verifies the port is free (not wanted while serving on it).
	CheckPort bool
}

// Run performs all checks.
func Run(ctx context.Context, cfg config.Config, paths config.Paths, opts Options) []Check {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	checks := []Check{
		claudeCheck(ctx, cfg.ClaudeBin),
		ghCheck(ctx, cfg.GHBin),
		gitCheck(ctx, cfg.GitBin),
		slkCheck(ctx, cfg.SlkBin),
		dataDirCheck(paths),
	}
	if opts.CheckPort {
		checks = append(checks, portCheck(cfg.Port))
	}
	return checks
}

// Healthy reports whether nothing failed.
func Healthy(checks []Check) bool {
	for _, c := range checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

func output(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func claudeCheck(ctx context.Context, bin string) Check {
	c := Check{Name: "claude"}
	v, err := output(ctx, bin, "--version")
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "Claude Code CLI not found", "install Claude Code: https://claude.com/claude-code"
		return c
	}
	out, err := output(ctx, bin, "auth", "status", "--json")
	var st struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err != nil || json.Unmarshal([]byte(out), &st) != nil || !st.LoggedIn {
		c.Status, c.Detail, c.Fix = Fail, v+", not signed in", "run `claude auth login`"
		return c
	}
	c.Status = OK
	c.Detail = fmt.Sprintf("%s, signed in via %s", v, st.AuthMethod)
	if st.SubscriptionType != "" {
		c.Detail += " (" + st.SubscriptionType + " plan: budgets are estimates, rate limits apply)"
	}
	return c
}

func ghCheck(ctx context.Context, bin string) Check {
	c := Check{Name: "gh"}
	v, err := output(ctx, bin, "--version")
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "GitHub CLI not found", "install gh: https://cli.github.com"
		return c
	}
	v = strings.SplitN(v, "\n", 2)[0]
	if _, err := output(ctx, bin, "auth", "status"); err != nil {
		c.Status, c.Detail, c.Fix = Fail, v+", not signed in", "run `gh auth login`"
		return c
	}
	c.Status, c.Detail = OK, v+", signed in"
	return c
}

func gitCheck(ctx context.Context, bin string) Check {
	c := Check{Name: "git"}
	v, err := output(ctx, bin, "--version")
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "git not found", "install git"
		return c
	}
	c.Status, c.Detail = OK, v
	return c
}

func slkCheck(ctx context.Context, bin string) Check {
	c := Check{Name: "slk"}
	if _, err := exec.LookPath(bin); err != nil {
		c.Status, c.Detail, c.Fix = Warn, "slk not found; Slack feedback intake is unavailable", "install slk: https://github.com/raulsh/slk"
		return c
	}
	out, err := output(ctx, bin, "auth", "test", "--json")
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err != nil || json.Unmarshal([]byte(out), &env) != nil || !env.OK {
		detail := "credentials are not working"
		if env.Error != nil {
			detail = env.Error.Message
		}
		c.Status, c.Detail, c.Fix = Warn, detail, "run `slk configure`"
		return c
	}
	c.Status, c.Detail = OK, "signed in to Slack"
	return c
}

func dataDirCheck(paths config.Paths) Check {
	c := Check{Name: "data directory"}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		c.Status, c.Detail = Fail, err.Error()
		return c
	}
	f, err := os.CreateTemp(paths.Root, ".doctor-*")
	if err != nil {
		c.Status, c.Detail = Fail, paths.Root+" is not writable"
		return c
	}
	f.Close()
	os.Remove(f.Name())
	c.Status, c.Detail = OK, paths.Root
	return c
}

func portCheck(port int) Check {
	c := Check{Name: "port"}
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		c.Status, c.Detail, c.Fix = Warn, fmt.Sprintf("127.0.0.1:%d is in use (is thefactory already running?)", port), "pick another port with --port"
		return c
	}
	l.Close()
	c.Status, c.Detail = OK, fmt.Sprintf("127.0.0.1:%d is free", port)
	return c
}
