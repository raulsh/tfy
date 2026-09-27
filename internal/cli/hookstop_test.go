package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookStopSendsBackUncommittedWork(t *testing.T) {
	root := t.TempDir()
	clean, dirty := filepath.Join(root, "api"), filepath.Join(root, "app")
	for _, d := range []string{clean, dirty} {
		if out, err := exec.Command("git", "init", "-q", d).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dirty, "left.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := runHookStop(strings.NewReader(`{"stop_hook_active": false}`), &stderr, []string{clean, dirty}); code != 2 {
		t.Fatalf("exit %d, want 2 with uncommitted work", code)
	}
	if msg := stderr.String(); !strings.Contains(msg, dirty) || strings.Contains(msg, clean) || !strings.Contains(msg, "commit conventions") {
		t.Errorf("message = %q", msg)
	}
	if code := runHookStop(strings.NewReader(`{"stop_hook_active": true}`), &stderr, []string{dirty}); code != 0 {
		t.Error("the hook asks once; the second stop goes through")
	}
	if code := runHookStop(strings.NewReader(`{}`), &stderr, []string{clean}); code != 0 {
		t.Error("clean checkouts may stop")
	}
	if code := runHookStop(strings.NewReader(`not json`), &stderr, []string{filepath.Join(root, "missing")}); code != 0 {
		t.Error("the hook never blocks on its own errors")
	}
}
