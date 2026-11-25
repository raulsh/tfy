package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// CLIClaude is Claude Code, the coding CLI tfy drives.
const CLIClaude = "claude"

// Kinds of account: how one signs in.
const (
	// AccountMachine is this machine's own Claude Code login, on whatever
	// provider its settings choose: how tfy ran before it had accounts.
	AccountMachine = "machine"
	// AccountToken is a Claude subscription's token, from `claude setup-token`.
	AccountToken = "token"
	// AccountAPIKey is an Anthropic API key.
	AccountAPIKey = "api_key"
	// AccountBedrock is Claude Code on Amazon Bedrock: a region, and an AWS
	// profile or a Bedrock API key.
	AccountBedrock = "bedrock"
)

// CLI is what tfy can do with one coding CLI.
type CLI struct {
	Name string `json:"name"`
	// Kinds are the ways its accounts sign in.
	Kinds []string `json:"kinds"`
	// Stages are the kinds of run it may run.
	Stages  []string `json:"stages"`
	Efforts []string `json:"efforts"`
	// Models are the usual choices; others may work too.
	Models []string `json:"models"`
}

// CLIs, by name.
var CLIs = map[string]CLI{
	CLIClaude: {
		Name:    CLIClaude,
		Kinds:   []string{AccountMachine, AccountToken, AccountAPIKey, AccountBedrock},
		Stages:  RunKinds,
		Efforts: Efforts,
		Models:  claudeAliases,
	},
}

// claudeAliases are the model aliases Claude Code resolves, on any provider.
var claudeAliases = []string{"fable", "opus", "sonnet", "haiku"}

// An account id names its secret file too: lowercase letters, digits and
// dashes only.
var accountID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidAccountID reports whether id can name an account.
func ValidAccountID(id string) bool { return accountID.MatchString(id) }

// NeedsSecret reports whether accounts of kind always need a secret. A
// Bedrock account needs one only without an AWS profile.
func NeedsSecret(kind string) bool { return kind == AccountToken || kind == AccountAPIKey }

// Account is a coding CLI and how it signs in.
type Account struct {
	ID       string
	Name     string
	CLI      string
	Kind     string
	Settings AccountSettings
	// PausedUntil is when the account's usage limit resets; zero if it is
	// not limited.
	PausedUntil time.Time
	// ModelBlocks are the models the account cannot run for now, by family
	// (ModelFamily), such as one its plan has no usage credits for.
	ModelBlocks map[string]ModelBlock
}

// Paused reports whether the account is still limited at t.
func (a Account) Paused(t time.Time) bool { return a.PausedUntil.After(t) }

// Blocked reports whether the account cannot run model at t, and why.
func (a Account) Blocked(model string, t time.Time) (ModelBlock, bool) {
	b, ok := a.ModelBlocks[ModelFamily(model)]
	if !ok {
		b, ok = a.ModelBlocks[model]
	}
	return b, ok && b.Until.After(t)
}

// Why an account cannot run a model for now.
const (
	// BlockCredits: its plan has no usage credits for the model.
	BlockCredits = "credits"
	// BlockLimit: the model's own usage window is used up, such as Fable's
	// weekly one. The account's other models still run.
	BlockLimit = "limit"
)

// ModelBlock keeps an account from running a model until a time.
type ModelBlock struct {
	Until time.Time `json:"until"`
	Cause string    `json:"cause"`
}

// UnmarshalJSON reads a block, or a bare time as blocks were first stored:
// those were all for want of credits.
func (b *ModelBlock) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		b.Cause = BlockCredits
		return json.Unmarshal(data, &b.Until)
	}
	type plain ModelBlock
	return json.Unmarshal(data, (*plain)(b))
}

// ParseModelBlocks decodes accounts.model_blocks, leaving out the blocks
// that ended before now.
func ParseModelBlocks(raw string, now time.Time) map[string]ModelBlock {
	var blocks map[string]ModelBlock
	_ = json.Unmarshal([]byte(raw), &blocks)
	out := map[string]ModelBlock{}
	for model, b := range blocks {
		if b.Until.After(now) {
			out[model] = b
		}
	}
	return out
}

// ModelFamily is the alias a model belongs to, such as fable for
// claude-fable-5-2 or a Bedrock id of it, or the model itself when it names
// none. A model's limits and credits are its family's: the CLI speaks of
// "your Fable limit".
func ModelFamily(model string) string {
	m := strings.ToLower(model)
	for _, alias := range claudeAliases {
		if strings.Contains(m, alias) {
			return alias
		}
	}
	return m
}

// Validate checks an account; hasSecret tells whether its secret is set.
func (a Account) Validate(hasSecret bool) error {
	if !ValidAccountID(a.ID) {
		return fmt.Errorf("an account id is lowercase letters, digits and dashes, not %q", a.ID)
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("an account needs a name")
	}
	cli, ok := CLIs[a.CLI]
	if !ok {
		return fmt.Errorf("unknown CLI %q", a.CLI)
	}
	if !slices.Contains(cli.Kinds, a.Kind) {
		return fmt.Errorf("a %s account signs in with one of %s, not %q", a.CLI, strings.Join(cli.Kinds, ", "), a.Kind)
	}
	switch {
	case NeedsSecret(a.Kind) && !hasSecret:
		return fmt.Errorf("a %s account needs its secret", strings.ReplaceAll(a.Kind, "_", " "))
	case a.Kind == AccountBedrock && a.Settings.Region == "":
		return errors.New("a Bedrock account needs its AWS region")
	case a.Kind == AccountBedrock && a.Settings.AWSProfile == "" && !hasSecret:
		return errors.New("a Bedrock account needs an AWS profile or a Bedrock API key")
	}
	for alias, model := range a.Settings.Models {
		if !slices.Contains(claudeAliases, alias) {
			return fmt.Errorf("models: %q is not one of %s", alias, strings.Join(claudeAliases, ", "))
		}
		if !modelName.MatchString(model) {
			return fmt.Errorf("models: %q is not a model name", model)
		}
	}
	return nil
}

// AccountSettings are an account's settings other than its secret.
type AccountSettings struct {
	// Region and AWSProfile are Bedrock's. Without a profile, the secret is
	// a Bedrock API key.
	Region     string `json:"region,omitempty"`
	AWSProfile string `json:"aws_profile,omitempty"`
	// Models maps an alias (opus, sonnet, haiku, fable) to the model the
	// account runs for it, such as a Bedrock model id.
	Models map[string]string `json:"models,omitempty"`
}

// Normalize trims the settings and drops empty models.
func (s AccountSettings) Normalize() AccountSettings {
	out := AccountSettings{Region: strings.TrimSpace(s.Region), AWSProfile: strings.TrimSpace(s.AWSProfile)}
	for alias, model := range s.Models {
		if model = strings.TrimSpace(model); model != "" {
			if out.Models == nil {
				out.Models = map[string]string{}
			}
			out.Models[strings.ToLower(strings.TrimSpace(alias))] = model
		}
	}
	return out
}

// ParseAccountSettings decodes accounts.settings.
func ParseAccountSettings(raw string) AccountSettings {
	var s AccountSettings
	_ = json.Unmarshal([]byte(raw), &s)
	return s.Normalize()
}

// JSON encodes the settings for storage.
func (s AccountSettings) JSON() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ModelFits reports whether model can be given to cli.
func ModelFits(cli, model string) bool {
	return cli == CLIClaude && modelName.MatchString(model)
}

// Assignment is where a run goes: the account it starts on, its model and
// effort there, and the accounts it may fail over to, the first one first.
type Assignment struct {
	Account Account
	Model   string
	Effort  string
	Pool    []Account
}

// Resolve assigns a run of kind. The unit's overrides win over the project's
// run settings, which win over the global ones. base is the config file's
// model and effort for the stage, which are Claude Code's.
//
// The candidates are the project's accounts, or the global ones, that can
// run kind, in order of preference. The run starts on the account the most
// specific setting names, or on the first candidate, and may fail over to
// the other candidates of the same CLI.
func Resolve(kind string, accounts []Account, global, project RunSettings, unit RunOverrides, base RunOverride) (Assignment, error) {
	byID := make(map[string]Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	ids := project.Accounts
	if len(ids) == 0 {
		ids = global.Accounts
	}
	var cand []Account
	for _, id := range ids {
		a, ok := byID[id]
		if !ok || slices.ContainsFunc(cand, func(c Account) bool { return c.ID == id }) {
			continue
		}
		if cli, ok := CLIs[a.CLI]; ok && slices.Contains(cli.Stages, kind) {
			cand = append(cand, a)
		}
	}
	if len(cand) == 0 {
		return Assignment{}, fmt.Errorf("no account can run %s: add one to the project's accounts or to the global ones", kind)
	}

	layers := []RunOverride{unit[kind], project.Stages[kind], global.Stages[kind]}
	as := Assignment{Account: cand[0]}
	for _, l := range layers {
		if i := slices.IndexFunc(cand, func(a Account) bool { return a.ID == l.Account }); l.Account != "" && i >= 0 {
			as.Account = cand[i]
			break
		}
	}
	cli := as.Account.CLI
	// A model or effort counts when it fits the account's CLI. One set next
	// to an account of another CLI was chosen for that CLI, and the config
	// file's are Claude Code's.
	if cli == CLIClaude {
		layers = append(layers, base)
	}
	for _, l := range layers {
		if l.Account != "" && byID[l.Account].CLI != cli {
			continue
		}
		if as.Model == "" && l.Model != "" && ModelFits(cli, l.Model) {
			as.Model = l.Model
		}
		if as.Effort == "" && l.Effort != "" && slices.Contains(CLIs[cli].Efforts, l.Effort) {
			as.Effort = l.Effort
		}
	}
	as.Pool = []Account{as.Account}
	for _, a := range cand {
		if a.ID != as.Account.ID && a.CLI == cli {
			as.Pool = append(as.Pool, a)
		}
	}
	return as, nil
}

// RunSettings choose the accounts runs use: the accounts in order of
// preference, and per kind of run an account, model or effort.
type RunSettings struct {
	Accounts []string     `json:"accounts"`
	Stages   RunOverrides `json:"stages"`
}

// Normalize trims the settings and drops repeated accounts. It rejects an
// invalid account id, and what Normalize rejects in the stages.
func (s RunSettings) Normalize() (RunSettings, error) {
	out := RunSettings{Accounts: []string{}}
	for _, id := range s.Accounts {
		id = strings.TrimSpace(id)
		if !ValidAccountID(id) {
			return RunSettings{}, fmt.Errorf("%q is not an account id", id)
		}
		if !slices.Contains(out.Accounts, id) {
			out.Accounts = append(out.Accounts, id)
		}
	}
	stages, err := s.Stages.normalize(RunKinds)
	if err != nil {
		return RunSettings{}, err
	}
	out.Stages = stages
	return out, nil
}

// ParseRunSettings decodes run settings, leaving out anything invalid.
func ParseRunSettings(raw string) RunSettings {
	var s RunSettings
	_ = json.Unmarshal([]byte(raw), &s)
	return s.lenient()
}

// lenient is Normalize that drops what is invalid instead of failing.
func (s RunSettings) lenient() RunSettings {
	out := RunSettings{Accounts: []string{}, Stages: RunOverrides{}}
	for _, id := range s.Accounts {
		if ValidAccountID(id) && !slices.Contains(out.Accounts, id) {
			out.Accounts = append(out.Accounts, id)
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(s.Stages)) {
		if n, err := (RunOverrides{kind: s.Stages[kind]}).normalize(RunKinds); err == nil {
			maps.Copy(out.Stages, n)
		}
	}
	return out
}

// JSON encodes the settings for storage.
func (s RunSettings) JSON() string {
	s = s.lenient()
	b, _ := json.Marshal(s)
	return string(b)
}

// Uses reports whether the settings name account id.
func (s RunSettings) Uses(id string) bool {
	return slices.Contains(s.Accounts, id) || slices.ContainsFunc(slices.Collect(maps.Values(s.Stages)), func(o RunOverride) bool { return o.Account == id })
}
