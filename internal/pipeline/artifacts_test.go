package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

func fileExists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil
}

// Only the regular files a run wrote are kept: a link, even a hard one,
// could bring in a file from outside the workspace, like a private key.
func TestReadArtifactsKeepsOnlyRegularFiles(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs", "artifacts")
	for name, body := range map[string]string{"mock.html": "<h1>mock</h1>", "flows/signup.md": "# Signup", ".scratch": "notes", ".cache/x": "x"} {
		full := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(secret, filepath.Join(dir, "key.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(dir, "home")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, filepath.Join(dir, "hard.txt")); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, maxArtifactSize+1)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	files, skipped, err := readArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || string(files["mock.html"]) != "<h1>mock</h1>" || string(files["flows/signup.md"]) != "# Signup" {
		t.Errorf("files = %v", keys(files))
	}
	for _, name := range []string{"key.txt", "home", "hard.txt", "big.bin"} {
		if !strings.Contains(strings.Join(skipped, "\n"), "artifact "+name+" left out") {
			t.Errorf("%s must be reported as left out: %v", name, skipped)
		}
	}

	// A link in place of the directory brings in nothing.
	link := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if files, _, err := readArtifacts(link); err != nil || len(files) != 0 {
		t.Errorf("through a linked directory: %v (%v)", keys(files), err)
	}
	if files, skipped, err := readArtifacts(filepath.Join(root, "none")); err != nil || len(files)+len(skipped) != 0 {
		t.Errorf("no directory: %v %v (%v)", files, skipped, err)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The plan's artifacts are kept with each run that changes them, can be the
// subject of a revision, and come back with the workspace when a rejected
// unit reopens.
func TestArtifactsFollowThePlan(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pr := h.project()
	control := filepath.Join(h.control, "artifacts")
	write := func(name, body string) {
		t.Helper()
		_ = os.MkdirAll(filepath.Dir(filepath.Join(control, name)), 0o755)
		if err := os.WriteFile(filepath.Join(control, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mock.html", "<h1>Health v1</h1>")
	write("flows/degraded.md", "# When the database is down")
	secret := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(secret, []byte("gho_secret"), 0o600)
	if err := os.Symlink(secret, filepath.Join(control, "token.txt")); err != nil {
		t.Fatal(err)
	}

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateSpecReview)
	latest := func() map[string]db.LatestArtifactsRow {
		rows, err := h.st.Q.LatestArtifacts(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]db.LatestArtifactsRow{}
		for _, a := range rows {
			out[a.Path] = a
		}
		return out
	}
	got := latest()
	if len(got) != 2 || got["mock.html"].Version != 1 || got["mock.html"].ContentType != "text/html; charset=utf-8" ||
		got["flows/degraded.md"].ContentType != "text/markdown; charset=utf-8" || got["mock.html"].RunID == "" {
		t.Fatalf("artifacts after planning = %+v", got)
	}
	if a, err := h.st.Q.GetArtifactVersion(ctx, db.GetArtifactVersionParams{UnitID: u.ID, Path: "mock.html", Version: 1}); err != nil || string(a.Content) != "<h1>Health v1</h1>" {
		t.Errorf("mock.html v1 = %q (%v)", a.Content, err)
	}

	// Feedback on one artifact: the revision is told which, changes it and
	// drops the other; only what changed gets a version.
	var invalid *InvalidError
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Bigger", Artifact: "nope.html"}); !errors.As(err, &invalid) {
		t.Fatalf("feedback on an unknown artifact: %v", err)
	}
	write("mock.html", "<h1>Health v2</h1>")
	_ = os.Remove(filepath.Join(control, "flows", "degraded.md"))
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Show the degraded banner in amber.", Artifact: "mock.html"}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateSpecReview)
	if prompt := h.read(filepath.Join(h.control, "prompt-plan.txt")); !strings.Contains(prompt, "The feedback is about docs/artifacts/mock.html") ||
		!strings.Contains(prompt, "Show the degraded banner in amber.") {
		t.Errorf("the revision must know which artifact:\n%s", prompt)
	}
	got = latest()
	if got["mock.html"].Version != 2 || !got["flows/degraded.md"].Removed || got["flows/degraded.md"].Version != 2 {
		t.Errorf("artifacts after the revision = %+v", got)
	}
	acts, _ := h.st.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 100})
	var log []string
	for _, a := range acts {
		log = append(log, a.Message)
	}
	for _, want := range []string{"about mock.html: Show the degraded banner in amber.", "artifact mock.html v2 by claude",
		"artifact flows/degraded.md removed by claude", "artifact token.txt left out: not a regular file"} {
		if !strings.Contains(strings.Join(log, "\n"), want) {
			t.Errorf("activity is missing %q:\n%s", want, strings.Join(log, "\n"))
		}
	}

	// Rejecting removes the workspace; reopened, the unit gets its
	// artifacts back.
	if _, err := h.p.Act(ctx, u.ID, ActionReject, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); h.p.Busy(ctx, u.ID); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not finish")
		}
	}
	if fileExists(u.WorkspacePath) {
		t.Fatal("rejecting removes the workspace")
	}
	if _, err := h.p.Act(ctx, u.ID, ActionReopen, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(u.WorkspacePath, ArtifactsDir, "mock.html")); err != nil || string(b) != "<h1>Health v2</h1>" {
		t.Errorf("reopened mock.html = %q (%v)", b, err)
	}
	if fileExists(filepath.Join(u.WorkspacePath, ArtifactsDir, "flows", "degraded.md")) {
		t.Error("a removed artifact must not come back")
	}
}
