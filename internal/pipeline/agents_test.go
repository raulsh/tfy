package pipeline

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/claude"
)

func boardFixture(t *testing.T, name string) []claude.Event {
	t.Helper()
	f, err := os.Open("../claude/testdata/" + name + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var evs []claude.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		evs = append(evs, claude.ParseLine(len(evs)+1, sc.Bytes()))
	}
	return evs
}

// The recorded session: the main agent starts two sub-agents in parallel,
// waits for them, then starts a third in the background (docs/spike.md).
func TestAgentBoardFollowsSubagents(t *testing.T) {
	var b agentBoard
	b.start(AgentRun{RunID: "r1", Kind: "develop", StartedAt: time.Now(), Subagents: true}, "/work/ws")
	at := time.Now()
	sawWaiting, most := false, 0
	for _, e := range boardFixture(t, "q10-subagents") {
		b.observe("r1", e, at)
		run := b.snapshot(2).Runs[0]
		if run.Main.Status == AgentWaiting {
			sawWaiting = true
		}
		working := 0
		for _, a := range run.Agents {
			if a.Status == AgentWorking {
				working++
			}
		}
		most = max(most, working)
	}
	if most != 2 {
		t.Errorf("at most %d sub-agents worked at once, want the two parallel ones", most)
	}
	if !sawWaiting {
		t.Error("the main agent never showed as waiting on its sub-agents")
	}

	run := b.snapshot(2).Runs[0]
	if len(run.Agents) != 3 {
		t.Fatalf("got %d sub-agents, want 3", len(run.Agents))
	}
	first := run.Agents[0]
	if first.Name != "Count repo-a files" || first.Type != "general-purpose" || first.ParentID != mainAgentID {
		t.Errorf("first sub-agent = %+v", first)
	}
	for _, a := range run.Agents {
		if a.Status != "completed" || a.EndedAt == nil || a.Model == "" || a.ToolUses == 0 || a.Report == "" {
			t.Errorf("sub-agent %q ended as %+v", a.Name, a)
		}
	}
	if !strings.Contains(first.Report, "BLOCKED") {
		t.Errorf("the report handed back is missing: %q", first.Report)
	}
	if run.Main.Status != AgentDone || run.Main.Model == "" {
		t.Errorf("main agent = %+v, want done", run.Main)
	}

	b.finish("r1")
	if n := len(b.snapshot(2).Runs); n != 0 {
		t.Errorf("a finished run stays on the board (%d runs)", n)
	}
}

// A sub-agent's own sub-agents hang under it.
func TestAgentBoardNestsSubagents(t *testing.T) {
	ev := func(line string) claude.Event { return claude.ParseLine(1, []byte(line)) }
	var b agentBoard
	b.start(AgentRun{RunID: "r1", Kind: "plan", StartedAt: time.Now()}, "/w")
	for _, e := range []claude.Event{
		ev(`{"type":"assistant","parent_tool_use_id":null,"message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Agent","input":{"description":"Survey"}}]}}`),
		ev(`{"type":"system","subtype":"task_started","task_id":"a1","tool_use_id":"t1","task_type":"local_agent","description":"Survey","subagent_type":"Explore"}`),
		ev(`{"type":"assistant","parent_tool_use_id":"t1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Agent","input":{"description":"Dig into the API"}}]}}`),
		ev(`{"type":"system","subtype":"task_started","task_id":"a2","tool_use_id":"t2","task_type":"local_agent","description":"Dig into the API","subagent_type":"Explore","spawn_depth":2}`),
		ev(`{"type":"system","subtype":"task_started","task_id":"b1","tool_use_id":"t9","task_type":"local_bash","description":"Serve"}`),
		ev(`{"type":"assistant","parent_tool_use_id":"t2","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"/w/api/main.go"}}]}}`),
	} {
		b.observe("r1", e, time.Now())
	}
	run := b.snapshot(2).Runs[0]
	if len(run.Agents) != 2 {
		t.Fatalf("got %d sub-agents, want 2 (a background command is not one)", len(run.Agents))
	}
	if run.Agents[1].ParentID != "a1" {
		t.Errorf("nested sub-agent's parent = %q, want a1", run.Agents[1].ParentID)
	}
	if run.Agents[0].Activity != "Agent: Dig into the API" {
		t.Errorf("parent sub-agent's activity = %q", run.Agents[0].Activity)
	}
	if run.Agents[1].Activity != "Read: api/main.go" {
		t.Errorf("paths in the run's directory should show relative: %q", run.Agents[1].Activity)
	}
}
