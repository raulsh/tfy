// Package config holds thefactory's configuration and on-disk layout.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Config is ~/.thefactory/config.yaml.
type Config struct {
	Port              int              `yaml:"port"`
	MaxConcurrentRuns int              `yaml:"max_concurrent_runs"`
	Stages            map[string]Stage `yaml:"stages"`
	PRPollInterval    time.Duration    `yaml:"pr_poll_interval"`
	SlackPollInterval time.Duration    `yaml:"slack_poll_interval"`

	// Executables, overridable for tests (THEFACTORY_CLAUDE_BIN, …).
	ClaudeBin string `yaml:"claude_bin"`
	GHBin     string `yaml:"gh_bin"`
	GitBin    string `yaml:"git_bin"`
	SlkBin    string `yaml:"slk_bin"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
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
		{"THEFACTORY_CLAUDE_BIN", &cfg.ClaudeBin, def.ClaudeBin},
		{"THEFACTORY_GH_BIN", &cfg.GHBin, def.GHBin},
		{"THEFACTORY_GIT_BIN", &cfg.GitBin, def.GitBin},
		{"THEFACTORY_SLK_BIN", &cfg.SlkBin, def.SlkBin},
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

// DefaultPaths uses $THEFACTORY_HOME or ~/.thefactory.
func DefaultPaths() (Paths, error) {
	if root := os.Getenv("THEFACTORY_HOME"); root != "" {
		abs, err := filepath.Abs(root)
		return Paths{Root: abs}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{Root: filepath.Join(home, ".thefactory")}, nil
}

func (p Paths) Config() string    { return filepath.Join(p.Root, "config.yaml") }
func (p Paths) DB() string        { return filepath.Join(p.Root, "factory.db") }
func (p Paths) Token() string     { return filepath.Join(p.Root, "token") }
func (p Paths) Lock() string      { return filepath.Join(p.Root, "serve.lock") }
func (p Paths) GuardBin() string  { return filepath.Join(p.Root, "bin", "thefactory") }
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
