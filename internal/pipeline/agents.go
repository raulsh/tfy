package pipeline

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/events"
	"github.com/raulsh/tfy/internal/store/db"
)

// AgentBoard is what every Claude run going now is doing: its main agent and
// the sub-agents it started. It lives in memory, like the runs themselves,
// and goes to the UI on the global topic as it changes.
type AgentBoard struct {
	MaxRuns int        `json:"max_runs"`
	Runs    []AgentRun `json:"runs"`
}

// AgentRun is one run on the board.
type AgentRun struct {
	RunID     string    `json:"run_id"`
	Kind      string    `json:"kind"`
	UnitID    string    `json:"unit_id,omitempty"`
	UnitLabel string    `json:"unit_label,omitempty"`
	UnitTitle string    `json:"unit_title,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Subagents tells whether the run may start sub-agents at all.
	Subagents bool    `json:"subagents"`
	Main      Agent   `json:"main"`
	Agents    []Agent `json:"agents"` // sub-agents, in the order they started
}

// Agent is the main agent of a run or one of its sub-agents.
type Agent struct {
	ID string `json:"id"` // the sub-agent's task id, or "main"
	// ParentID is the agent that started this one.
	ParentID string `json:"parent_id,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"` // the sub-agent type, e.g. Explore
	Model    string `json:"model,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	// Status is working, waiting (on its sub-agents) or done, or for a
	// sub-agent that ended, how: completed, failed, killed or stopped.
	Status    string     `json:"status"`
	Activity  string     `json:"activity,omitempty"` // what it is doing now
	ActiveAt  time.Time  `json:"active_at"`
	ToolUses  int        `json:"tool_uses"`
	Tokens    int64      `json:"tokens,omitempty"`
	Report    string     `json:"report,omitempty"` // what it handed back
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Agent statuses tfy sets; ended sub-agents carry the CLI's.
const (
	AgentWorking = "working"
	AgentWaiting = "waiting"
	AgentDone    = "done"
)

// mainAgentID names a run's own agent on the board.
const mainAgentID = "main"

// boardFlushDelay coalesces a burst of events into one update.
const boardFlushDelay = 250 * time.Millisecond

// agentBoard tracks the runs going now. The zero value is ready to use.
type agentBoard struct {
	mu      sync.Mutex
	runs    map[string]*boardRun
	pending bool // an update is scheduled
}

type boardRun struct {
	AgentRun
	agents    []*Agent          // sub-agents, in the order they started
	byTask    map[string]*Agent // task id → sub-agent
	byToolUse map[string]*Agent // the Agent call that started it → sub-agent
	// agentCalls maps each Agent call to the call of the sub-agent that
	// made it ("" when the main agent did): nested sub-agents hang there.
	agentCalls map[string]string
	// openCalls are the main agent's tool calls still waiting on a result.
	openCalls map[string]string // tool use id → tool
	lastTool  string            // the main agent's last tool, "" after text
	idle      bool              // the session reported a result and did not resume
	cwd       string            // paths under it show relative
}

// activity describes a tool call, with paths in the run's directory made
// relative to it.
func (r *boardRun) activity(c claude.ContentBlock) string {
	if r.cwd == "" {
		return c.Activity()
	}
	return strings.ReplaceAll(c.Activity(), r.cwd+"/", "")
}

// Agents returns the board as it is now.
func (p *Pipeline) Agents() AgentBoard {
	return p.board.snapshot(p.Config.MaxConcurrentRuns)
}

// start puts a run, working in cwd, on the board.
func (b *agentBoard) start(r AgentRun, cwd string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runs == nil {
		b.runs = map[string]*boardRun{}
	}
	r.Main = Agent{ID: mainAgentID, Name: r.Kind, Status: AgentWorking, StartedAt: r.StartedAt, ActiveAt: r.StartedAt, Activity: "Starting"}
	b.runs[r.RunID] = &boardRun{
		AgentRun: r, byTask: map[string]*Agent{}, byToolUse: map[string]*Agent{},
		agentCalls: map[string]string{}, openCalls: map[string]string{}, cwd: cwd,
	}
}

// finish takes a run off the board.
func (b *agentBoard) finish(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.runs, runID)
}

// observe applies one of a run's events, and reports whether the board
// changed.
func (b *agentBoard) observe(runID string, e claude.Event, at time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.runs[runID]
	if r == nil {
		return false
	}
	switch e.Type {
	case claude.TypeAssistant:
		msg, ok := e.Message()
		if !ok {
			return false
		}
		parent := e.ParentToolUseID()
		a := &r.Main
		if parent != "" {
			if a = r.byToolUse[parent]; a == nil {
				return false
			}
		} else {
			r.idle = false
		}
		if a.Model == "" {
			a.Model = msg.Model
		}
		for _, c := range msg.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					a.Activity, a.ActiveAt = clip(strings.Join(strings.Fields(t), " "), 200), at
					if parent == "" {
						r.lastTool = ""
					}
				}
			case "tool_use":
				a.ToolUses++
				a.ActiveAt = at
				switch c.Name {
				case claude.AgentTool:
					r.agentCalls[c.ID] = parent
					a.Activity = r.activity(c)
				case "SubagentHandback":
					var in struct {
						Message string `json:"message"`
					}
					_ = json.Unmarshal(c.Input, &in)
					a.Activity, a.Report = "Reporting back", clip(strings.TrimSpace(in.Message), 600)
				default:
					a.Activity = r.activity(c)
				}
				if parent == "" {
					r.openCalls[c.ID], r.lastTool = c.Name, c.Name
				}
			}
		}
	case claude.TypeUser:
		if e.ParentToolUseID() != "" {
			return false
		}
		if l, ok := e.SubagentLaunch(); ok {
			if a := r.byTask[l.AgentID]; a != nil && a.Model == "" {
				a.Model = l.Model
			}
		}
		msg, ok := e.Message()
		if !ok {
			return false
		}
		for _, c := range msg.Content {
			if c.Type == "tool_result" {
				delete(r.openCalls, c.ToolUseID)
			}
		}
	case claude.TypeResult:
		r.idle = true
	case claude.TypeSystem:
		t, ok := e.Task()
		if !ok {
			return false
		}
		switch e.Subtype {
		case claude.SubTaskStarted:
			if t.TaskType != claude.TaskTypeAgent {
				return false
			}
			parentID := mainAgentID
			if p := r.byToolUse[r.agentCalls[t.ToolUseID]]; p != nil {
				parentID = p.ID
			}
			a := &Agent{
				ID: t.TaskID, ParentID: parentID, Name: strings.TrimSpace(t.Description), Type: t.SubagentType,
				Prompt: clip(strings.TrimSpace(t.Prompt), 600), Status: AgentWorking, Activity: "Starting",
				StartedAt: at, ActiveAt: at,
			}
			r.agents = append(r.agents, a)
			r.byTask[t.TaskID] = a
			r.byToolUse[t.ToolUseID] = a
		default:
			a := r.byTask[t.TaskID]
			if a == nil {
				return false
			}
			if u := t.Usage; u != nil {
				a.ToolUses, a.Tokens = max(a.ToolUses, u.ToolUses), u.TotalTokens
			}
			if s := t.Ended(); s != "" && a.EndedAt == nil {
				end := at
				a.Status, a.EndedAt, a.Activity = s, &end, ""
			}
		}
	default:
		return false
	}
	return true
}

// snapshot copies the board, deriving the main agents' status.
func (b *agentBoard) snapshot(maxRuns int) AgentBoard {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := AgentBoard{MaxRuns: maxRuns, Runs: []AgentRun{}}
	for _, r := range b.runs {
		run := r.AgentRun
		run.Agents = make([]Agent, 0, len(r.agents))
		working := 0
		for _, a := range r.agents {
			run.Agents = append(run.Agents, *a)
			if a.EndedAt == nil {
				working++
			}
		}
		run.Main.Status = r.mainStatus(working)
		out.Runs = append(out.Runs, run)
	}
	slices.SortFunc(out.Runs, func(x, y AgentRun) int { return x.StartedAt.Compare(y.StartedAt) })
	return out
}

// mainStatus tells what the main agent is up to: done once the session is
// idle, waiting when all it has left is its sub-agents' work, working
// otherwise.
func (r *boardRun) mainStatus(working int) string {
	switch {
	case r.idle && working == 0:
		return AgentDone
	case working == 0:
		return AgentWorking
	}
	for _, tool := range r.openCalls {
		if tool != claude.AgentTool {
			return AgentWorking
		}
	}
	if r.lastTool == "" || r.lastTool == claude.AgentTool {
		return AgentWaiting
	}
	return AgentWorking
}

// boardChanged schedules an update of the board for the UI, coalescing
// bursts of events.
func (p *Pipeline) boardChanged() {
	b := &p.board
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending {
		return
	}
	b.pending = true
	time.AfterFunc(boardFlushDelay, func() {
		b.mu.Lock()
		b.pending = false
		b.mu.Unlock()
		p.Hub.PublishJSON(events.TopicGlobal, "agents", "", p.Agents())
	})
}

// unitLabel is the unit's label, or "" for runs without a unit.
func unitLabel(u db.Unit) string {
	if u.ID == "" {
		return ""
	}
	return domain.Label(u.Seq)
}
