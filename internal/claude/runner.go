package claude

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Status is how a run ended.
type Status string

const (
	StatusSucceeded      Status = "succeeded"
	StatusFailed         Status = "failed"
	StatusCancelled      Status = "cancelled"
	StatusTimedOut       Status = "timed_out"
	StatusBudgetExceeded Status = "budget_exceeded"
	StatusRateLimited    Status = "rate_limited"
	// StatusAborted means the runner stopped the run for safety: the guard
	// failed, the permission mode was wrong, or the agent pushed.
	StatusAborted Status = "aborted"
)

// Outcome is the result of Runner.Run.
type Outcome struct {
	Status    Status
	Reason    string // why the run did not succeed
	ExitCode  int
	PID       int
	Init      *Init
	Result    *Result
	RateLimit *RateLimit // the last one seen
	Denials   int
	Stderr    string // tail
	StartedAt time.Time
	EndedAt   time.Time
}

// Callbacks observe a run while it happens. Both are optional and are called
// from the goroutine running Run.
type Callbacks struct {
	OnStart func(pid int)
	OnEvent func(Event)
}

// Runner starts claude processes.
type Runner struct {
	Bin string
	Log *slog.Logger

	// PostResultGrace is how long the process may linger after its result
	// event (it waits on background tasks) before it is stopped.
	PostResultGrace time.Duration
	// InterruptGrace separates SIGINT from SIGTERM, TerminateGrace SIGTERM
	// from SIGKILL. SIGINT goes first: it still yields a result event.
	InterruptGrace time.Duration
	TerminateGrace time.Duration
}

func (r *Runner) bin() string {
	if r.Bin != "" {
		return r.Bin
	}
	return "claude"
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Run executes spec and blocks until the process has exited and its output is
// consumed. It never returns nil.
func (r *Runner) Run(ctx context.Context, spec *Spec, cb Callbacks) *Outcome {
	out := &Outcome{StartedAt: time.Now()}
	defer func() { out.EndedAt = time.Now() }()

	if err := spec.Validate(); err != nil {
		out.Status, out.Reason = StatusFailed, "invalid run: "+err.Error()
		return out
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		out.Status, out.Reason = StatusFailed, "stdout pipe: "+err.Error()
		return out
	}
	stderr := newTail(64 << 10)

	cmd := exec.Command(r.bin(), spec.Args()...)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	if cmd.Env == nil {
		cmd.Env = BuildEnv(EnvOptions{})
	}
	cmd.Stdin = strings.NewReader(spec.Prompt)
	// A plain *os.File, not StdoutPipe: Wait closes StdoutPipe as soon as the
	// process exits, which can drop the final lines still being read.
	cmd.Stdout = pw
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGTERM, // if thefactory dies, so does the run
	}

	started := make(chan error, 1)
	exited := make(chan struct{})
	var waitErr error
	go func() {
		// Pdeathsig fires when the OS thread that forked exits, not the
		// process; keep this goroutine on its thread until the child is done.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		waitErr = cmd.Wait()
		close(exited)
	}()
	if err := <-started; err != nil {
		pr.Close()
		pw.Close()
		out.Status, out.Reason = StatusFailed, "start claude: "+err.Error()
		return out
	}
	pw.Close() // the child holds its own copy
	pgid := cmd.Process.Pid
	out.PID = pgid
	r.log().Debug("claude started", "pid", pgid, "cwd", spec.Cwd, "cmd", spec.String())
	if cb.OnStart != nil {
		cb.OnStart(pgid)
	}

	events := make(chan Event, 64)
	go readEvents(pr, events)

	mon := newMonitor(spec)
	var (
		timeoutC   <-chan time.Time
		graceC     <-chan time.Time
		drainC     <-chan time.Time
		ctxDone    = ctx.Done()
		exitedC    = (<-chan struct{})(exited)
		stopOnce   sync.Once
		cancelled  bool
		timedOut   bool
		abortWhy   string
		procExited bool
	)
	stop := func() {
		stopOnce.Do(func() { go r.escalate(pgid, exited) })
	}
	if spec.Timeout > 0 {
		t := time.NewTimer(spec.Timeout)
		defer t.Stop()
		timeoutC = t.C
	}

	for events != nil || !procExited {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if cb.OnEvent != nil {
				cb.OnEvent(ev)
			}
			if why := mon.observe(ev); why != "" && abortWhy == "" {
				abortWhy = why
				r.log().Warn("aborting claude run", "pid", pgid, "reason", why)
				stop()
			}
			if ev.Type == TypeResult && graceC == nil {
				graceC = time.After(orDefault(r.PostResultGrace, 30*time.Second))
			}
		case <-ctxDone:
			ctxDone = nil
			// Once the result is in, the work is done: stopping a lingering
			// process is not a cancellation.
			cancelled = mon.result == nil
			stop()
		case <-timeoutC:
			timeoutC = nil
			timedOut = mon.result == nil
			stop()
		case <-graceC:
			graceC = nil
			stop()
		case <-exitedC:
			exitedC = nil
			procExited = true
			// A descendant that inherited stdout can keep the pipe open;
			// give the reader a moment, then stop waiting for it.
			drainC = time.After(2 * time.Second)
		case <-drainC:
			drainC = nil
			pr.Close()
		}
	}
	pr.Close()
	// Take down anything the run left behind in its process group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	out.ExitCode = exitCode(waitErr)
	out.Init, out.Result, out.RateLimit, out.Denials = mon.init, mon.result, mon.rateLimit, mon.denials
	out.Stderr = stderr.String()

	res := mon.result
	switch {
	case abortWhy != "":
		out.Status, out.Reason = StatusAborted, abortWhy
	case cancelled:
		out.Status, out.Reason = StatusCancelled, "cancelled"
	case timedOut:
		out.Status, out.Reason = StatusTimedOut, fmt.Sprintf("exceeded the %s time limit", spec.Timeout)
	case res == nil:
		out.Status = StatusFailed
		out.Reason = fmt.Sprintf("claude exited with code %d without a result", out.ExitCode)
		if tail := lastLines(out.Stderr, 5); tail != "" {
			out.Reason += ": " + tail
		}
	case strings.Contains(res.Subtype, "budget"):
		out.Status, out.Reason = StatusBudgetExceeded, "the run hit its budget"
	case res.IsError && mon.rateLimited:
		out.Status, out.Reason = StatusRateLimited, "rate limited"
	case res.IsError:
		out.Status = StatusFailed
		out.Reason = res.Subtype
		if t := strings.TrimSpace(res.Result); t != "" {
			out.Reason += ": " + clip(oneLine(t), 300)
		}
	default:
		out.Status = StatusSucceeded
	}
	return out
}

// escalate stops the process group: SIGINT, then SIGTERM, then SIGKILL.
func (r *Runner) escalate(pgid int, exited <-chan struct{}) {
	steps := []struct {
		sig  syscall.Signal
		wait time.Duration
	}{
		{syscall.SIGINT, orDefault(r.InterruptGrace, 10*time.Second)},
		{syscall.SIGTERM, orDefault(r.TerminateGrace, 5*time.Second)},
		{syscall.SIGKILL, 0},
	}
	for _, s := range steps {
		if err := syscall.Kill(-pgid, s.sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			r.log().Warn("signal claude", "pid", pgid, "signal", s.sig, "error", err)
		}
		if s.wait == 0 {
			return
		}
		select {
		case <-exited:
			return
		case <-time.After(s.wait):
		}
	}
}

func readEvents(pr *os.File, events chan<- Event) {
	defer close(events)
	sc := bufio.NewScanner(pr)
	// Tool results can be large; bufio's 64 KiB default kills runs.
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	seq := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		seq++
		events <- ParseLine(seq, line)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTail(max int) *tail { return &tail{max: max} }

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Purge deletes Claude Code's stored state for a project directory:
// transcripts, file history, and its config entry.
func (r *Runner) Purge(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, r.bin(), "project", "purge", "-y", dir)
	cmd.Env = BuildEnv(EnvOptions{})
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude project purge: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
