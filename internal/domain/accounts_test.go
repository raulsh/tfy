package domain

import (
	"slices"
	"strings"
	"testing"
)

func ids(as []Account) []string {
	out := []string{}
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func TestResolve(t *testing.T) {
	accounts := []Account{
		{ID: "machine", CLI: CLIClaude, Kind: AccountMachine},
		{ID: "sub1", CLI: CLIClaude, Kind: AccountToken},
		{ID: "sub2", CLI: CLIClaude, Kind: AccountToken},
		{ID: "bedrock", CLI: CLIClaude, Kind: AccountBedrock},
	}
	global := RunSettings{Accounts: []string{"machine"}, Stages: RunOverrides{"review": {Effort: "low"}}}
	project := RunSettings{
		Accounts: []string{"sub1", "sub2", "bedrock"},
		Stages:   RunOverrides{"develop": {Account: "sub2", Model: "sonnet"}, "plan": {Account: "bedrock"}},
	}
	base := RunOverride{Model: "opus", Effort: "high"}
	cases := []struct {
		name    string
		kind    string
		global  RunSettings
		project RunSettings
		unit    RunOverrides
		account string
		model   string
		effort  string
		pool    []string
	}{
		{name: "the global list when the project has none", kind: "define", global: global, account: "machine", model: "opus", effort: "high", pool: []string{"machine"}},
		{name: "the project's first account", kind: "define", global: global, project: project, account: "sub1", model: "opus", effort: "high", pool: []string{"sub1", "sub2", "bedrock"}},
		{name: "the stage's account, then the rest in the project's order", kind: "develop", global: global, project: project, account: "sub2", model: "sonnet", effort: "high", pool: []string{"sub2", "sub1", "bedrock"}},
		{name: "the unit wins", kind: "develop", global: global, project: project, unit: RunOverrides{"develop": {Account: "bedrock", Effort: "max"}}, account: "bedrock", model: "sonnet", effort: "max", pool: []string{"bedrock", "sub1", "sub2"}},
		{name: "global stage settings still apply", kind: "review", global: global, project: project, account: "sub1", model: "opus", effort: "low", pool: []string{"sub1", "sub2", "bedrock"}},
		{name: "an account outside the candidates is ignored", kind: "define", global: global, project: project, unit: RunOverrides{"define": {Account: "machine"}}, account: "sub1", model: "opus", effort: "high", pool: []string{"sub1", "sub2", "bedrock"}},
		{name: "triage is a kind of run too", kind: "triage", global: global, project: project, account: "sub1", model: "opus", effort: "high", pool: []string{"sub1", "sub2", "bedrock"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as, err := Resolve(c.kind, accounts, c.global, c.project, c.unit, base)
			if err != nil {
				t.Fatal(err)
			}
			if as.Account.ID != c.account || as.Model != c.model || as.Effort != c.effort || !slices.Equal(ids(as.Pool), c.pool) {
				t.Errorf("got %s %s %s %v, want %s %s %s %v", as.Account.ID, as.Model, as.Effort, ids(as.Pool), c.account, c.model, c.effort, c.pool)
			}
		})
	}

	if _, err := Resolve("define", accounts, RunSettings{Accounts: []string{"gone"}}, RunSettings{}, nil, base); err == nil || !strings.Contains(err.Error(), "no account can run define") {
		t.Errorf("no candidate: %v", err)
	}
	// Repeated and unknown ids in a list are skipped.
	as, err := Resolve("define", accounts, RunSettings{Accounts: []string{"gone", "sub1", "sub1", "machine"}}, RunSettings{}, nil, base)
	if err != nil || !slices.Equal(ids(as.Pool), []string{"sub1", "machine"}) {
		t.Errorf("pool %v (%v)", ids(as.Pool), err)
	}
}

func TestAccountValidate(t *testing.T) {
	ok := []struct {
		a      Account
		secret bool
	}{
		{Account{ID: "machine", Name: "This machine", CLI: CLIClaude, Kind: AccountMachine}, false},
		{Account{ID: "sub-2", Name: "Work", CLI: CLIClaude, Kind: AccountToken}, true},
		{Account{ID: "bedrock", Name: "Bedrock", CLI: CLIClaude, Kind: AccountBedrock, Settings: AccountSettings{Region: "us-east-1", AWSProfile: "dev"}}, false},
		{Account{ID: "bedrock-key", Name: "Bedrock", CLI: CLIClaude, Kind: AccountBedrock, Settings: AccountSettings{Region: "us-east-1", Models: map[string]string{"fable": "us.anthropic.claude-opus-5-5"}}}, true},
	}
	for _, c := range ok {
		if err := c.a.Validate(c.secret); err != nil {
			t.Errorf("%s: %v", c.a.ID, err)
		}
	}
	bad := []struct {
		a      Account
		secret bool
		want   string
	}{
		{Account{ID: "../etc", Name: "x", CLI: CLIClaude, Kind: AccountMachine}, false, "account id"},
		{Account{ID: "x", CLI: CLIClaude, Kind: AccountMachine}, false, "needs a name"},
		{Account{ID: "x", Name: "x", CLI: "cursor", Kind: AccountMachine}, false, "unknown CLI"},
		{Account{ID: "x", Name: "x", CLI: CLIClaude, Kind: "login"}, false, "signs in with"},
		{Account{ID: "x", Name: "x", CLI: CLIClaude, Kind: AccountToken}, false, "needs its secret"},
		{Account{ID: "x", Name: "x", CLI: CLIClaude, Kind: AccountBedrock, Settings: AccountSettings{AWSProfile: "dev"}}, false, "region"},
		{Account{ID: "x", Name: "x", CLI: CLIClaude, Kind: AccountBedrock, Settings: AccountSettings{Region: "us-east-1"}}, false, "profile or a Bedrock API key"},
		{Account{ID: "x", Name: "x", CLI: CLIClaude, Kind: AccountBedrock, Settings: AccountSettings{Region: "r", AWSProfile: "p", Models: map[string]string{"gpt": "x"}}}, false, "is not one of"},
	}
	for _, c := range bad {
		if err := c.a.Validate(c.secret); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.a, err, c.want)
		}
	}
}

func TestRunSettings(t *testing.T) {
	s, err := RunSettings{Accounts: []string{" sub1", "sub1", "machine"}, Stages: RunOverrides{"triage": {Account: "sub1"}, "plan": {}}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Accounts, []string{"sub1", "machine"}) || len(s.Stages) != 1 || s.Stages["triage"].Account != "sub1" {
		t.Errorf("normalized %+v", s)
	}
	if !s.Uses("sub1") || !s.Uses("machine") || s.Uses("bedrock") {
		t.Error("Uses")
	}
	if _, err := (RunSettings{Accounts: []string{"Not An Id"}}).Normalize(); err == nil {
		t.Error("an invalid account id must be refused")
	}
	if _, err := (RunSettings{Stages: RunOverrides{"deploy": {Model: "opus"}}}).Normalize(); err == nil {
		t.Error("an unknown kind of run must be refused")
	}
	// Stored settings with something invalid lose only that part.
	got := ParseRunSettings(`{"accounts":["sub1","Bad Id"],"stages":{"develop":{"account":"sub1","effort":"extreme"},"plan":{"model":"opus"}}}`)
	if !slices.Equal(got.Accounts, []string{"sub1"}) || len(got.Stages) != 1 || got.Stages["plan"].Model != "opus" {
		t.Errorf("parsed %+v", got)
	}
	if p := ParseProjectSettings(`{"runs":{"accounts":["sub1"]}}`); !slices.Equal(p.Runs.Accounts, []string{"sub1"}) {
		t.Errorf("project runs %+v", p.Runs)
	}
}
