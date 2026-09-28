package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/spf13/cobra"

	"github.com/raulsh/tfy/internal/api"
	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/config"
	"github.com/raulsh/tfy/internal/doctor"
	"github.com/raulsh/tfy/internal/events"
	"github.com/raulsh/tfy/internal/gh"
	"github.com/raulsh/tfy/internal/git"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/pipeline"
	"github.com/raulsh/tfy/internal/slack"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/web"
)

type serveOpts struct {
	host    string
	port    int
	open    bool
	dev     bool
	verbose bool
}

func newServeCmd() *cobra.Command {
	var o serveOpts
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run tfy and serve its UI (on 127.0.0.1 unless --host says otherwise)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), o)
		},
	}
	cmd.Flags().StringVar(&o.host, "host", "", "address to listen on (default from config, 127.0.0.1); 0.0.0.0 serves the local network")
	cmd.Flags().IntVar(&o.port, "port", 0, "port to listen on (default from config, 7420)")
	cmd.Flags().BoolVar(&o.open, "open", false, "open the UI in a browser")
	cmd.Flags().BoolVar(&o.dev, "dev", false, "allow the Vite dev server (localhost:5174) to call the API")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false, "debug logging")
	return cmd
}

func runServe(ctx context.Context, o serveOpts) error {
	level := slog.LevelInfo
	if o.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	if o.host != "" {
		cfg.Host = o.host
	}
	if o.port != 0 {
		cfg.Port = o.port
	}
	if err := cfg.CheckHost(); err != nil {
		return err
	}
	if err := migrateLegacyHome(ctx, paths); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return err
	}
	unlock, err := lockInstance(paths.Lock())
	if err != nil {
		return err
	}
	defer unlock()
	token, err := ensureToken(paths.Token())
	if err != nil {
		return err
	}
	if err := installGuard(paths.GuardBin()); err != nil {
		return fmt.Errorf("install the guard hook binary: %w", err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	checks := doctor.Run(ctx, cfg, paths, doctor.Options{CheckPort: true})
	for _, c := range checks {
		if c.Status != doctor.OK {
			log.Warn("dependency check", "check", c.Name, "status", c.Status, "detail", c.Detail, "fix", c.Fix)
		}
	}
	if !doctor.Healthy(checks) {
		return errors.New("fix the failed checks above (see `tfy doctor`)")
	}

	st, err := store.Open(ctx, paths.DB())
	if err != nil {
		return err
	}
	defer st.Close()

	hub := events.NewHub()
	var slk *slack.Client
	if _, err := exec.LookPath(cfg.SlkBin); err == nil {
		slk = &slack.Client{Bin: cfg.SlkBin}
	} else {
		log.Warn("slk not found; Slack intake is off", "bin", cfg.SlkBin)
	}
	queue := jobs.New(st, cfg.MaxConcurrentRuns, log.With("component", "jobs"))
	p := pipeline.New(pipeline.Deps{
		Store:  st,
		Config: cfg,
		Paths:  paths,
		Runner: &claude.Runner{Bin: cfg.ClaudeBin, Log: log.With("component", "claude")},
		Git:    &git.Git{Bin: cfg.GitBin},
		GH:     &gh.Client{Bin: cfg.GHBin},
		Slack:  slk,
		Hub:    hub,
		Jobs:   queue,
		Log:    log.With("component", "pipeline"),
	})
	name, email := gitIdentityParts()
	if err := p.PrepareAgentEnv(name, email); err != nil {
		return err
	}
	if err := p.Recover(ctx); err != nil {
		return fmt.Errorf("recover from the previous run: %w", err)
	}

	workCtx, stopWork := context.WithCancel(context.WithoutCancel(ctx))
	workDone := make(chan struct{})
	go func() {
		queue.Run(workCtx)
		close(workDone)
	}()
	p.Start(workCtx)

	var devOrigins []string
	if o.dev {
		devOrigins = []string{"http://localhost:5174", "http://127.0.0.1:5174"}
	}
	app := api.New(api.Options{
		Pipeline: p, Store: st, Hub: hub, Jobs: queue, Config: cfg, Paths: paths, Token: token,
		Version: version, DevOrigins: devOrigins, UI: web.FS(), Log: log.With("component", "api"),
	})

	ln, err := cfg.Listen()
	if err != nil {
		stopWork()
		return fmt.Errorf("listen on %s: %w", cfg.Addr(), err)
	}
	if !cfg.LoopbackOnly() {
		log.Warn("tfy is reachable from the network over plain HTTP: anyone with the link can drive it, so share it only on networks you trust", "addr", cfg.Addr())
	}
	local, lan := serveLinks(cfg, token)
	fmt.Fprintf(os.Stderr, "\n  tfy is running\n\n  open  %s\n", local)
	if lan != "" {
		fmt.Fprintf(os.Stderr, "  lan   %s\n", lan)
	}
	fmt.Fprintf(os.Stderr, "  data  %s\n\n", paths.Root)
	if o.open {
		go openBrowser(local)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			log.Error("server stopped", "error", err)
		}
	}

	log.Info("shutting down")
	_ = app.ShutdownWithTimeout(5 * time.Second)
	// Running Claude sessions are interrupted (SIGINT) and recorded; jobs
	// with side effects are reconciled at the next start.
	stopWork()
	select {
	case <-workDone:
	case <-time.After(30 * time.Second):
		log.Warn("gave up waiting for running work to stop")
	}
	return nil
}

// serveLinks returns the link that opens tfy on this machine and, when it
// listens on every interface, the one for other machines on the network.
func serveLinks(cfg config.Config, token string) (local, lan string) {
	link := func(host string) string {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port)) + "/?token=" + token
	}
	if ip := net.ParseIP(cfg.Host); ip == nil || !ip.IsUnspecified() {
		return link(cfg.Host), ""
	}
	// Dialing UDP sends nothing; it only picks the address the default
	// route leaves by, which skips container bridges.
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		ip := c.LocalAddr().(*net.UDPAddr).IP
		c.Close()
		if !ip.IsLoopback() {
			lan = link(ip.String())
		}
	}
	return link("127.0.0.1"), lan
}

// migrateLegacyHome moves the data directory thefactory used (~/.thefactory)
// to ~/.tfy, once, and rewrites the absolute paths stored in the database.
func migrateLegacyHome(ctx context.Context, paths config.Paths) error {
	old, ok := config.LegacyRoot(paths)
	if !ok {
		return nil
	}
	// A running thefactory holds its lock; moving the directory under it
	// would lose its writes.
	unlock, err := lockInstance(filepath.Join(old, "serve.lock"))
	if err != nil {
		return fmt.Errorf("%s is in use by a running thefactory: stop it, then start tfy again", old)
	}
	unlock()
	if err := os.Rename(old, paths.Root); err != nil {
		return fmt.Errorf("move %s to %s: %w", old, paths.Root, err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := filepath.Join(paths.Root, config.LegacyDB+suffix)
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, paths.DB()+suffix); err != nil {
				return err
			}
		}
	}
	_ = os.Remove(filepath.Join(paths.Root, "bin", "thefactory"))
	st, err := store.Open(ctx, paths.DB())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.RewritePathPrefix(ctx, old, paths.Root); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "moved %s to %s\n", old, paths.Root)
	return nil
}

// lockInstance makes sure only one tfy serves a data directory.
func lockInstance(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("tfy is already running for %s", filepath.Dir(path))
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// ensureToken reads the per-install token, creating it on first use.
func ensureToken(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(b)) >= 32 {
		return string(bytes.TrimSpace(b)), nil
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	return token, os.WriteFile(path, []byte(token+"\n"), 0o600)
}

// installGuard copies this executable to a stable path for the guard hook:
// under `go run` or air the running binary lives in a temporary directory.
func installGuard(dst string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	src, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, src) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tfy-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, bytes.NewReader(src)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func gitIdentityParts() (string, string) {
	get := func(key string) string {
		out, err := exec.Command("git", "config", "--global", "--get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
}

func openBrowser(url string) {
	for _, bin := range []string{"xdg-open", "open", "wslview"} {
		if _, err := exec.LookPath(bin); err == nil {
			_ = exec.Command(bin, url).Start()
			return
		}
	}
}
