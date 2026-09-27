// Package guard decides whether a shell command an agent wants to run may
// run. It backs `thefactory hook-guard`, the PreToolUse hook on every factory
// run with shell access.
//
// Its job is narrow: publishing belongs to the orchestrator, so agents may not
// push, touch remotes, rewrite credential or remote configuration, or use the
// GitHub CLI. Everything else is left to the permission mode. The guard is one
// layer of several — agents also run without publishing credentials — so it
// errs towards simple, explainable rules over completeness.
package guard

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Input is the JSON a PreToolUse hook receives on stdin.
type Input struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	SessionID      string `json:"session_id"`
}

// Decision is the guard's verdict.
type Decision struct {
	Allow  bool
	Reason string
}

func allow() Decision { return Decision{Allow: true} }

func deny(format string, args ...any) Decision {
	return Decision{Reason: fmt.Sprintf(format, args...)}
}

// Check decides on a hook input. Only shell-running tools are inspected.
func Check(in Input) Decision {
	switch in.ToolName {
	case "Bash", "Monitor":
		return CheckCommand(in.ToolInput.Command)
	}
	return allow()
}

// CheckCommand decides on one shell command line.
func CheckCommand(cmd string) Decision {
	return checkScript(cmd, 0)
}

// maxDepth bounds `sh -c` / eval nesting.
const maxDepth = 4

func checkScript(script string, depth int) Decision {
	if depth > maxDepth {
		return deny("command nests shells too deeply to check")
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		// Bash would most likely reject it too. Only refuse when it looks
		// like it could be doing something the guard exists to stop.
		if riskyText.MatchString(script) {
			return deny("could not parse this command to check it; write it more simply")
		}
		return allow()
	}
	verdict := allow()
	syntax.Walk(f, func(n syntax.Node) bool {
		if !verdict.Allow {
			return false
		}
		if call, ok := n.(*syntax.CallExpr); ok {
			verdict = checkCall(call, depth)
		}
		return true
	})
	return verdict
}

var riskyText = regexp.MustCompile(`\b(push|remote|gh|credential|config)\b`)

// word is a command-line argument as far as it can be known statically.
type word struct {
	s   string
	lit bool // fully literal: no expansions
}

func render(w *syntax.Word) word {
	var sb strings.Builder
	lit := true
	var walk func(parts []syntax.WordPart)
	walk = func(parts []syntax.WordPart) {
		for _, p := range parts {
			switch p := p.(type) {
			case *syntax.Lit:
				sb.WriteString(p.Value)
			case *syntax.SglQuoted:
				sb.WriteString(p.Value)
			case *syntax.DblQuoted:
				walk(p.Parts)
			case *syntax.ParamExp:
				lit = false
				if p.Param != nil {
					sb.WriteString("$" + p.Param.Value)
				} else {
					sb.WriteString("$?")
				}
			default:
				lit = false
				sb.WriteString("$(…)")
			}
		}
	}
	walk(w.Parts)
	return word{s: sb.String(), lit: lit}
}

func checkCall(call *syntax.CallExpr, depth int) Decision {
	args := make([]word, 0, len(call.Args))
	for _, a := range call.Args {
		args = append(args, render(a))
	}
	args = unwrap(args)
	if len(args) == 0 {
		return allow()
	}
	name := path.Base(args[0].s)

	// A command name that is not literal (`$G push`) cannot be resolved;
	// refuse only when it carries the argument the guard cares about.
	if !args[0].lit {
		for _, a := range args[1:] {
			if a.s == "push" {
				return deny("indirect command with a push argument is not allowed: pushing is done by thefactory")
			}
		}
	}

	switch name {
	case "sh", "bash", "zsh", "dash", "ksh":
		for i := 1; i < len(args)-1; i++ {
			if args[i].s == "-c" || (strings.HasPrefix(args[i].s, "-") && strings.HasSuffix(args[i].s, "c") && !strings.HasPrefix(args[i].s, "--")) {
				return checkScript(args[i+1].s, depth+1)
			}
		}
	case "eval":
		parts := make([]string, 0, len(args)-1)
		for _, a := range args[1:] {
			parts = append(parts, a.s)
		}
		return checkScript(strings.Join(parts, " "), depth+1)
	case "gh", "hub":
		return deny("the %s CLI is not available to agents: thefactory opens and updates pull requests itself", name)
	case "git":
		return checkGit(args[1:])
	}
	return allow()
}

// wrappers run the rest of their arguments as a command. The value is the
// set of their options that take a separate argument.
var wrappers = map[string]map[string]bool{
	"env":      {"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true},
	"command":  {},
	"builtin":  {},
	"exec":     {"-a": true},
	"nohup":    {},
	"time":     {"-f": true, "--format": true, "-o": true, "--output": true},
	"nice":     {"-n": true, "--adjustment": true},
	"ionice":   {"-c": true, "-n": true, "-p": true},
	"timeout":  {"-s": true, "--signal": true, "-k": true, "--kill-after": true},
	"sudo":     {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true},
	"doas":     {"-u": true, "-C": true},
	"xargs":    {"-a": true, "-d": true, "-E": true, "-I": true, "-L": true, "-n": true, "-P": true, "-s": true, "--arg-file": true, "--delimiter": true, "--max-args": true, "--max-procs": true, "--replace": true},
	"stdbuf":   {"-i": true, "-o": true, "-e": true},
	"setsid":   {},
	"chronic":  {},
	"unbuffer": {},
}

// positional counts the non-option arguments a wrapper consumes before the
// command it runs.
var positional = map[string]int{"timeout": 1}

// unwrap strips wrappers such as `env FOO=1`, `timeout 30`, `xargs -n1` down
// to the command they run.
func unwrap(args []word) []word {
	for len(args) > 0 {
		opts, ok := wrappers[path.Base(args[0].s)]
		if !ok {
			return args
		}
		name := path.Base(args[0].s)
		args = args[1:]
		skip := positional[name]
		for len(args) > 0 {
			a := args[0].s
			switch {
			case a == "--":
				args = args[1:]
				goto next
			case strings.HasPrefix(a, "-") && len(a) > 1:
				args = args[1:]
				if opts[a] && len(args) > 0 {
					args = args[1:]
				}
			case name == "env" && strings.Contains(a, "="):
				args = args[1:]
			case skip > 0:
				skip--
				args = args[1:]
			default:
				goto next
			}
		}
	next:
	}
	return args
}

// gitGlobalWithArg are git's global options that take a separate value.
var gitGlobalWithArg = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--exec-path": true, "--config-env": true, "--super-prefix": true,
}

// protectedConfig matches configuration keys that control where git talks to
// and with which credentials, or that could alias a push.
var protectedConfig = regexp.MustCompile(`(?i)^(remote\.|url\.|credential|alias\.|core\.sshcommand|core\.hookspath|http\..*extraheader|http\.proxy|pushurl)`)

func checkGit(args []word) Decision {
	i := 0
	for i < len(args) {
		a := args[i].s
		if !strings.HasPrefix(a, "-") {
			break
		}
		name, value, hasValue := strings.Cut(a, "=")
		if name == "-c" || name == "--config-env" {
			v := value
			if !hasValue && i+1 < len(args) {
				v = args[i+1].s
			}
			if key, _, _ := strings.Cut(v, "="); protectedConfig.MatchString(key) {
				return deny("git -c %s is not allowed: remotes and credentials are managed by thefactory", key)
			}
		}
		if gitGlobalWithArg[name] && !hasValue {
			i++
		}
		i++
	}
	if i >= len(args) {
		return allow()
	}
	sub, rest := args[i].s, args[i+1:]
	switch sub {
	case "push", "send-pack", "http-push", "send-email", "request-pull":
		return deny("git %s is not allowed: commit locally and stop — thefactory pushes and opens the pull request", sub)
	case "remote":
		for _, r := range rest {
			switch r.s {
			case "add", "set-url", "rename", "remove", "rm", "set-head", "set-branches", "prune":
				return deny("git remote %s is not allowed: checkouts have no remotes on purpose", r.s)
			}
		}
	case "config":
		if isConfigRead(rest) {
			return allow()
		}
		for _, r := range rest {
			if r.s == "--global" || r.s == "--system" {
				return deny("git config %s is not allowed", r.s)
			}
			if key, _, _ := strings.Cut(r.s, "="); !strings.HasPrefix(key, "-") && protectedConfig.MatchString(key) {
				return deny("git config %s is not allowed: remotes and credentials are managed by thefactory", key)
			}
		}
	}
	return allow()
}

// isConfigRead reports whether `git config` args only read configuration, in
// either the option form (--get, --list) or the subcommand form (get, list).
func isConfigRead(args []word) bool {
	if len(args) > 0 && (args[0].s == "get" || args[0].s == "list") {
		return true
	}
	for _, a := range args {
		switch a.s {
		case "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list", "-l":
			return true
		}
	}
	return false
}
