package domain

import (
	"strings"
	"testing"
)

func TestRunOverrides(t *testing.T) {
	o, err := RunOverrides{
		"develop": {Model: " sonnet ", Effort: "LOW"},
		"review":  {Effort: "max"},
		"plan":    {Model: "claude-opus-5-5"},
		"merge":   {}, // left empty in the form
		"learn":   {Model: "opus[1m]", Effort: " "},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	want := RunOverrides{
		"develop": {Model: "sonnet", Effort: "low"},
		"review":  {Effort: "max"},
		"plan":    {Model: "claude-opus-5-5"},
		"learn":   {Model: "opus[1m]"},
	}
	if o.JSON() != want.JSON() {
		t.Errorf("normalized = %s, want %s", o.JSON(), want.JSON())
	}
	if back := ParseRunOverrides(o.JSON()); back.JSON() != o.JSON() {
		t.Errorf("round trip = %s", back.JSON())
	}

	for _, bad := range []RunOverrides{
		{"triage": {Model: "sonnet"}},
		{"deploy": {Effort: "low"}},
		{"develop": {Effort: "extreme"}},
		{"develop": {Model: "--dangerously-skip-permissions"}},
		{"develop": {Model: "opus --effort max"}},
		{"develop": {Model: strings.Repeat("a", 101)}},
	} {
		if _, err := bad.Normalize(); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}

	// What is stored and no longer valid is left out, not fatal.
	if got := ParseRunOverrides(`{"develop":{"effort":"extreme"},"review":{"model":"sonnet"}}`); got.JSON() != `{"review":{"model":"sonnet"}}` {
		t.Errorf("parsed = %s", got.JSON())
	}
	if got := ParseRunOverrides(""); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
}
