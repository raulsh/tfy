package claude

import (
	"os"
	"strings"
)

// passthroughEnv lists the variables a run inherits: what the CLI needs to
// authenticate and what common toolchains need to build and test. Credentials
// that could publish anything — GH_TOKEN, SSH_AUTH_SOCK, cloud keys — are
// deliberately absent.
var passthroughEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR",
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR",
	// Claude Code's own credentials, for API-key and token setups.
	"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR",
	// Toolchains.
	"GOPATH", "GOROOT", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GOFLAGS", "GOMODCACHE", "GOCACHE", "GOTOOLCHAIN",
	"CARGO_HOME", "RUSTUP_HOME",
	"NVM_DIR", "FNM_DIR", "FNM_MULTISHELL_PATH", "FNM_VERSION_FILE_STRATEGY", "FNM_NODE_DIST_MIRROR", "PNPM_HOME", "BUN_INSTALL", "VOLTA_HOME",
	"JAVA_HOME", "PYENV_ROOT", "UV_CACHE_DIR", "PIP_INDEX_URL",
	// Networking.
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"DOCKER_HOST",
}

// EnvOptions shapes the environment of a run.
type EnvOptions struct {
	// GitConfigGlobal points at a gitconfig with the commit identity and no
	// credential helper.
	GitConfigGlobal string
	// GHConfigDir points at an empty directory so `gh` has no token.
	GHConfigDir string
	// Extra entries, "KEY=value", appended last.
	Extra []string
}

// BuildEnv returns the environment for a run, built from scratch rather than
// inherited so no publishing credential leaks into the agent.
func BuildEnv(opts EnvOptions) []string {
	env := make([]string, 0, len(passthroughEnv)+12)
	for _, k := range passthroughEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	env = append(env,
		"TERM=dumb",
		"NO_COLOR=1",
		// Auto memory is shared by every checkout of a repo; runs must not
		// leak context into each other.
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
		// Every shell command starts in the run's working directory. Left to
		// persist, a `cd` into a checkout moves the directory the CLI
		// resolves relative permission rules against: `Write(./docs/**)`
		// then no longer covers the unit's docs (docs/spike.md).
		"CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1",
		"GIT_TERMINAL_PROMPT=0",
		// No agent, no default identities: ssh can't authenticate a push.
		"GIT_SSH_COMMAND=ssh -F /dev/null -o IdentitiesOnly=yes -o IdentityFile=/dev/null -o BatchMode=yes",
	)
	if opts.GitConfigGlobal != "" {
		env = append(env, "GIT_CONFIG_GLOBAL="+opts.GitConfigGlobal, "GIT_CONFIG_NOSYSTEM=1")
	}
	if opts.GHConfigDir != "" {
		env = append(env, "GH_CONFIG_DIR="+opts.GHConfigDir)
	}
	env = append(env, opts.Extra...)
	return dedupeEnv(env)
}

// dedupeEnv keeps the last value of each key, as exec would.
func dedupeEnv(env []string) []string {
	idx := make(map[string]int, len(env))
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	return out
}
