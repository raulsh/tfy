package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings is the subset of Claude Code settings a factory run generates and
// passes with --settings. Everything else comes from nowhere: runs use
// --setting-sources "".
type Settings struct {
	// DisableAllHooks is written explicitly false so nothing can switch the
	// guard off.
	DisableAllHooks bool                     `json:"disableAllHooks"`
	Hooks           map[string][]HookMatcher `json:"hooks,omitempty"`
	Permissions     *Permissions             `json:"permissions,omitempty"`
	AutoMode        *AutoMode                `json:"autoMode,omitempty"`
	Attribution     *Attribution             `json:"attribution,omitempty"`
}

// Attribution is the text Claude Code adds to commits and pull requests.
// Empty strings add nothing (no Co-Authored-By trailer).
type Attribution struct {
	Commit string `json:"commit"`
	PR     string `json:"pr"`
}

// HookMatcher binds hooks to the tools whose name matches Matcher.
type HookMatcher struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

// Hook is a command hook.
type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

// Permissions holds allow and deny rules.
type Permissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// AutoMode carries auto mode classifier context. It is only honoured from
// flag and user settings, never from a repo's.
type AutoMode struct {
	Environment []string `json:"environment,omitempty"`
}

// GuardedTools are the tools that run shell commands, and so go through the
// guard hook. Monitor runs commands too.
const GuardedTools = "Bash|Monitor"

// DenyRules are a second layer behind the guard: prefix rules alone are easy
// to sidestep (`git -C . push` passes `Bash(git push *)`).
var DenyRules = []string{
	"Bash(git push *)",
	"Bash(git push)",
	"Bash(gh *)",
	"Bash(git remote add *)",
	"Bash(git remote set-url *)",
	"Bash(git config --global *)",
}

// GuardSettings builds settings that route every shell command through
// guardCommand (e.g. `/home/u/.thefactory/bin/thefactory hook-guard --stage
// develop`). trustedRepos, when set, tells auto mode which checkouts under
// the working directory are in scope.
func GuardSettings(guardCommand string, trustedRepos []string) Settings {
	s := Settings{
		DisableAllHooks: false,
		Hooks: map[string][]HookMatcher{
			"PreToolUse": {{
				Matcher: GuardedTools,
				Hooks:   []Hook{{Type: "command", Command: guardCommand, Timeout: 30}},
			}},
		},
		Permissions: &Permissions{Deny: DenyRules},
	}
	if len(trustedRepos) > 0 {
		s.AutoMode = &AutoMode{Environment: []string{
			"$defaults",
			fmt.Sprintf("**Trusted repo**: every git checkout directly under the working directory (%s) belongs to this task; editing, building, testing and committing in any of them is in scope. They have no remotes and must never be pushed: publishing is done by the orchestrator.",
				strings.Join(trustedRepos, ", ")),
		}}
	}
	return s
}

// WriteSettings writes s as JSON to path, creating parent directories.
func WriteSettings(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
