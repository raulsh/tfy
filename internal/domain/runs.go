package domain

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// UnitRunKinds are the kinds of Claude run a unit has, in pipeline order.
// Triage is not one: it runs before there is a unit.
var UnitRunKinds = []string{"define", "plan", "develop", "review", "merge", "release", "learn", "issue"}

// Efforts are the CLI's effort levels.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// A model is a CLI alias (opus) or a full name (claude-opus-5-5, opus[1m],
// or a provider's id with dots and colons). It never starts with a dash,
// so it cannot pass for a flag.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\[\]-]{0,99}$`)

// RunOverride replaces the configured model or effort of one kind of run,
// for one unit. An empty field keeps the configuration's.
type RunOverride struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// RunOverrides are a unit's overrides, by kind of run.
type RunOverrides map[string]RunOverride

// Normalize trims the overrides and drops the empty ones. It rejects an
// unknown kind of run, model or effort level.
func (o RunOverrides) Normalize() (RunOverrides, error) {
	out := RunOverrides{}
	for _, kind := range slices.Sorted(maps.Keys(o)) {
		v := RunOverride{Model: strings.TrimSpace(o[kind].Model), Effort: strings.ToLower(strings.TrimSpace(o[kind].Effort))}
		if v == (RunOverride{}) {
			continue
		}
		if !slices.Contains(UnitRunKinds, kind) {
			return nil, fmt.Errorf("a unit has no %q run", kind)
		}
		if v.Model != "" && !modelName.MatchString(v.Model) {
			return nil, fmt.Errorf("%s: %q is not a model name", kind, v.Model)
		}
		if v.Effort != "" && !slices.Contains(Efforts, v.Effort) {
			return nil, fmt.Errorf("%s: effort is one of %s, not %q", kind, strings.Join(Efforts, ", "), v.Effort)
		}
		out[kind] = v
	}
	return out, nil
}

// ParseRunOverrides decodes units.run_overrides, leaving out anything
// invalid.
func ParseRunOverrides(raw string) RunOverrides {
	var o RunOverrides
	if raw == "" || json.Unmarshal([]byte(raw), &o) != nil {
		return RunOverrides{}
	}
	out := RunOverrides{}
	for kind, v := range o {
		if n, err := (RunOverrides{kind: v}).Normalize(); err == nil {
			maps.Copy(out, n)
		}
	}
	return out
}

// JSON encodes the overrides for storage.
func (o RunOverrides) JSON() string {
	if len(o) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(o)
	return string(b)
}
