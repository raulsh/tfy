package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// passthroughEnv lists the variables a run inherits: what common toolchains
// need to build and test. Credentials that could publish anything — GH_TOKEN,
// SSH_AUTH_SOCK, cloud keys — are deliberately absent; the CLI's own reach it
// alone (credentialEnv).
var passthroughEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR",
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR",
	// Claude Code's configuration.
	"CLAUDE_CONFIG_DIR",
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

// providerEnv lists what points the CLI at a provider other than Anthropic's
// API, signs it in there, and names the provider's models for the aliases a
// stage asks for. The CLI needs them; the agent's commands start without
// them (ShellEnv).
var providerEnv = []string{
	// Amazon Bedrock.
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_SKIP_BEDROCK_AUTH", "ANTHROPIC_BEDROCK_BASE_URL", "AWS_BEARER_TOKEN_BEDROCK",
	"AWS_REGION", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
	"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION",
	// Google Vertex AI.
	"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_SKIP_VERTEX_AUTH", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_VERTEX_PROJECT_ID",
	"CLOUD_ML_REGION", "GOOGLE_APPLICATION_CREDENTIALS",
	// Microsoft Foundry.
	"CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_SKIP_FOUNDRY_AUTH", "ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE", "ANTHROPIC_FOUNDRY_API_KEY",
	// An LLM gateway.
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS",
	// The models.
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
}

// credentialEnv lists what signs the CLI in: Anthropic's credentials, then
// the provider's. The CLI inherits them; the agent's commands start without
// them (ShellEnv).
var credentialEnv = append([]string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}, providerEnv...)

// EnvOptions shapes the environment of a run.
type EnvOptions struct {
	// GitConfigGlobal points at a gitconfig with the commit identity and no
	// credential helper.
	GitConfigGlobal string
	// GHConfigDir points at an empty directory so `gh` has no token.
	GHConfigDir string
	// ShellEnvFile holds ShellEnv, which the CLI runs ahead of each of the
	// agent's shell commands (CLAUDE_ENV_FILE).
	ShellEnvFile string
	// Extra entries, "KEY=value", appended last.
	Extra []string
}

// BuildEnv returns the environment for a run, built from scratch rather than
// inherited so no publishing credential leaks into the agent.
func BuildEnv(opts EnvOptions) []string {
	env := make([]string, 0, len(passthroughEnv)+len(credentialEnv)+12)
	for _, k := range passthroughEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for _, k := range credentialEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	// Runs don't load the user's settings, yet that is where the CLI keeps a
	// provider chosen with it. Its values win, as in the user's sessions.
	env = append(env, userCredentialEnv()...)
	if opts.ShellEnvFile != "" {
		env = append(env, "CLAUDE_ENV_FILE="+opts.ShellEnvFile)
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

// userCredentialEnv returns the credentialEnv entries of the env in Claude
// Code's user settings.
func userCredentialEnv() []string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		dir = filepath.Join(home, ".claude")
	}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return nil
	}
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(b, &settings) != nil {
		return nil
	}
	var env []string
	for _, k := range credentialEnv {
		if v, ok := settings.Env[k]; ok && v != nil {
			env = append(env, k+"="+fmt.Sprint(v))
		}
	}
	return env
}

// ShellEnv is the script for EnvOptions.ShellEnvFile. The CLI runs it in the
// shell of each of the agent's commands, ahead of the command, so neither the
// commands nor the tests and tools they start act with the CLI's
// credentials.
func ShellEnv() string {
	return "unset " + strings.Join(credentialEnv, " ") + "\n"
}

// WriteShellEnv writes ShellEnv to path.
func WriteShellEnv(path string) error {
	return os.WriteFile(path, []byte(ShellEnv()), 0o644)
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
