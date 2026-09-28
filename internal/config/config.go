// Package config holds tfy's configuration and on-disk layout.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Stage settings for one kind of Claude run.
type Stage struct {
	Model   string        `yaml:"model"`
	Effort  string        `yaml:"effort"`
	Budget  float64       `yaml:"budget_usd"`
	Timeout time.Duration `yaml:"timeout"`
}

// Config is ~/.tfy/config.yaml.
type Config struct {
	// Host is the address `tfy serve` listens on: 127.0.0.1 keeps tfy on
	// this machine, 0.0.0.0 serves the local network.
	Host              string           `yaml:"host"`
	Port              int              `yaml:"port"`
	MaxConcurrentRuns int              `yaml:"max_concurrent_runs"`
	Stages            map[string]Stage `yaml:"stages"`
	PRPollInterval    time.Duration    `yaml:"pr_poll_interval"`
	SlackPollInterval time.Duration    `yaml:"slack_poll_interval"`

	// Executables, overridable for tests (TFY_CLAUDE_BIN, …).
	ClaudeBin string `yaml:"claude_bin"`
	GHBin     string `yaml:"gh_bin"`
	GitBin    string `yaml:"git_bin"`
	SlkBin    string `yaml:"slk_bin"`

	// CommitAttribution keeps Claude Code's Co-Authored-By trailer on the
	// commits agents make. Off by default.
	CommitAttribution bool `yaml:"commit_attribution"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Host:              "127.0.0.1",
		Port:              7420,
		MaxConcurrentRuns: 2,
		PRPollInterval:    time.Minute,
		SlackPollInterval: 2 * time.Minute,
		Stages: map[string]Stage{
			"triage":  {Model: "sonnet", Effort: "low", Budget: 1, Timeout: 5 * time.Minute},
			"define":  {Model: "opus", Effort: "high", Budget: 3, Timeout: 20 * time.Minute},
			"plan":    {Model: "opus", Effort: "high", Budget: 5, Timeout: 30 * time.Minute},
			"develop": {Model: "opus", Effort: "high", Budget: 20, Timeout: 90 * time.Minute},
			"review":  {Model: "opus", Effort: "high", Budget: 5, Timeout: 30 * time.Minute},
			"release": {Model: "sonnet", Effort: "low", Budget: 1, Timeout: 10 * time.Minute},
			"learn":   {Model: "opus", Effort: "medium", Budget: 2, Timeout: 15 * time.Minute},
			"issue":   {Model: "opus", Effort: "medium", Budget: 1, Timeout: 10 * time.Minute},
		},
		ClaudeBin: "claude",
		GHBin:     "gh",
		GitBin:    "git",
		SlkBin:    "slk",
	}
}

// Stage returns the settings for a run kind, falling back to the defaults
// field by field.
func (c Config) Stage(kind string) Stage {
	def := Default().Stages[kind]
	s, ok := c.Stages[kind]
	if !ok {
		return def
	}
	if s.Model == "" {
		s.Model = def.Model
	}
	if s.Effort == "" {
		s.Effort = def.Effort
	}
	if s.Budget <= 0 {
		s.Budget = def.Budget
	}
	if s.Timeout <= 0 {
		s.Timeout = def.Timeout
	}
	return s
}

// Load reads the config file under paths.Root, applying defaults for anything
// unset and environment overrides last.
func Load(p Paths) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(p.Config())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return cfg, err
	default:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", p.Config(), err)
		}
	}
	def := Default()
	if cfg.Host == "" {
		cfg.Host = def.Host
	}
	if cfg.Port == 0 {
		cfg.Port = def.Port
	}
	if cfg.MaxConcurrentRuns <= 0 {
		cfg.MaxConcurrentRuns = def.MaxConcurrentRuns
	}
	if cfg.PRPollInterval <= 0 {
		cfg.PRPollInterval = def.PRPollInterval
	}
	if cfg.SlackPollInterval <= 0 {
		cfg.SlackPollInterval = def.SlackPollInterval
	}
	bins := []struct {
		env string
		val *string
		def string
	}{
		{"TFY_CLAUDE_BIN", &cfg.ClaudeBin, def.ClaudeBin},
		{"TFY_GH_BIN", &cfg.GHBin, def.GHBin},
		{"TFY_GIT_BIN", &cfg.GitBin, def.GitBin},
		{"TFY_SLK_BIN", &cfg.SlkBin, def.SlkBin},
	}
	for _, b := range bins {
		if e := os.Getenv(b.env); e != "" {
			*b.val = e
		} else if *b.val == "" {
			*b.val = b.def
		}
	}
	return cfg, nil
}

// CheckHost reports whether Host is an address tfy can listen on.
func (c Config) CheckHost() error {
	if c.Host != "localhost" && net.ParseIP(c.Host) == nil {
		return fmt.Errorf("host %q: use an IP address, such as 127.0.0.1 or 0.0.0.0", c.Host)
	}
	return nil
}

// Addr is the address `tfy serve` listens on.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// LoopbackOnly reports whether Host keeps tfy on this machine.
func (c Config) LoopbackOnly() bool {
	if c.Host == "localhost" {
		return true
	}
	ip := net.ParseIP(c.Host)
	return ip != nil && ip.IsLoopback()
}

// Listen opens the listener `tfy serve` uses: IPv4 only for an IPv4 Host,
// so 0.0.0.0 means what it says.
func (c Config) Listen() (net.Listener, error) {
	network := "tcp"
	if ip := net.ParseIP(c.Host); c.Host == "localhost" || ip.To4() != nil {
		network = "tcp4"
	}
	return net.Listen(network, c.Addr())
}

// Save writes cfg to the config file.
func Save(p Paths, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.Config(), data, 0o600)
}

// Paths is the on-disk layout under the data directory.
type Paths struct {
	Root string
}

// DefaultPaths uses $TFY_HOME or ~/.tfy.
func DefaultPaths() (Paths, error) {
	if root := os.Getenv("TFY_HOME"); root != "" {
		abs, err := filepath.Abs(root)
		return Paths{Root: abs}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{Root: filepath.Join(home, ".tfy")}, nil
}

// legacyDirName is the data directory of tfy's first name, thefactory.
const legacyDirName = ".thefactory"

// LegacyRoot returns the data directory left by thefactory when it should be
// moved to paths: the default location is used (no TFY_HOME), the new one
// does not exist yet, and the old one does.
func LegacyRoot(p Paths) (string, bool) {
	if os.Getenv("TFY_HOME") != "" {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || p.Root != filepath.Join(home, ".tfy") {
		return "", false
	}
	if _, err := os.Stat(p.Root); !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	old := filepath.Join(home, legacyDirName)
	if fi, err := os.Stat(old); err != nil || !fi.IsDir() {
		return "", false
	}
	return old, true
}

// LegacyDB is the database file name under the old data directory.
const LegacyDB = "factory.db"

func (p Paths) Config() string    { return filepath.Join(p.Root, "config.yaml") }
func (p Paths) DB() string        { return filepath.Join(p.Root, "tfy.db") }
func (p Paths) Token() string     { return filepath.Join(p.Root, "token") }
func (p Paths) Lock() string      { return filepath.Join(p.Root, "serve.lock") }
func (p Paths) GuardBin() string  { return filepath.Join(p.Root, "bin", "tfy") }
func (p Paths) GitConfig() string { return filepath.Join(p.Root, "agent", "gitconfig") }
func (p Paths) GHConfig() string  { return filepath.Join(p.Root, "agent", "gh") }
func (p Paths) Repos() string     { return filepath.Join(p.Root, "repos") }
func (p Paths) Workspaces() string {
	return filepath.Join(p.Root, "workspaces")
}

// RunDir holds per-run files the agent must not see, such as its settings.
func (p Paths) RunDir(runID string) string { return filepath.Join(p.Root, "runs", runID) }

// ManagedClone is the fetch-only bare clone of a repository.
func (p Paths) ManagedClone(fullName string) string {
	return filepath.Join(p.Repos(), filepath.FromSlash(fullName)+".git")
}
