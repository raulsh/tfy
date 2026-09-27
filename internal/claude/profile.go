package claude

// Profile is the tool and permission shape of one kind of tfy run.
type Profile struct {
	Name           string
	Tools          []string // exact built-in tool set; empty means none
	AllowedTools   []string
	PermissionMode string
	// Guarded profiles have shell access: they get the guard hook, and the
	// runner aborts if the guard does not answer.
	Guarded    bool
	ForbidPush bool
	MaxDenials int
	// Persist keeps the session on disk so a later run can --resume it.
	Persist bool
}

// docsOnly lets a run write documents under ./docs and nothing else. Read-only
// shell commands (ls, rg, cat, find) are allowed by dontAsk on their own.
var docsOnly = []string{"Read", "Edit(./docs/**)", "Write(./docs/**)"}

// Profiles, by run kind. The tool names are those of Claude Code 2.1.283,
// which has no Glob or Grep tools (search goes through Bash) and exposes
// scheduling and remote tools by default that no tfy run gets.
var Profiles = map[string]Profile{
	"triage": {
		Name:           "triage",
		Tools:          []string{},
		PermissionMode: ModeDontAsk,
	},
	"define": {
		Name:           "define",
		Tools:          []string{"Read", "Write", "Edit"},
		AllowedTools:   docsOnly,
		PermissionMode: ModeDontAsk,
		MaxDenials:     20,
		Persist:        true,
	},
	"plan": {
		Name:           "plan",
		Tools:          []string{"Bash", "Read", "Write", "Edit"},
		AllowedTools:   docsOnly,
		PermissionMode: ModeDontAsk,
		Guarded:        true,
		ForbidPush:     true,
		MaxDenials:     40,
		Persist:        true,
	},
	"develop": {
		Name:           "develop",
		Tools:          []string{"Bash", "Read", "Write", "Edit", "NotebookEdit", "WebFetch", "WebSearch"},
		PermissionMode: ModeAuto,
		Guarded:        true,
		ForbidPush:     true,
		MaxDenials:     40,
		Persist:        true,
	},
	"review": {
		Name:           "review",
		Tools:          []string{"Bash", "Read"},
		AllowedTools:   []string{"Read"},
		PermissionMode: ModeDontAsk,
		Guarded:        true,
		ForbidPush:     true,
		MaxDenials:     40,
	},
	// learn looks back at a finished unit and may propose changes to the
	// repositories' conventions. Like review, it only reads.
	"learn": {
		Name:           "learn",
		Tools:          []string{"Bash", "Read"},
		AllowedTools:   []string{"Read"},
		PermissionMode: ModeDontAsk,
		Guarded:        true,
		ForbidPush:     true,
		MaxDenials:     40,
	},
	"release": {
		Name:           "release",
		Tools:          []string{},
		PermissionMode: ModeDontAsk,
	},
}

// Apply copies the profile's tool and permission shape onto spec.
func (p Profile) Apply(spec *Spec) {
	spec.Tools = append([]string{}, p.Tools...)
	spec.AllowedTools = append([]string(nil), p.AllowedTools...)
	spec.PermissionMode = p.PermissionMode
	spec.RequireGuard = p.Guarded
	spec.ForbidPush = p.ForbidPush
	spec.MaxDenials = p.MaxDenials
	if !p.Persist && spec.SessionID == "" && spec.ResumeSession == "" {
		spec.NoSessionPersistence = true
	}
}
