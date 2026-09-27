package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newHookStopCmd() *cobra.Command {
	var dirs []string
	cmd := &cobra.Command{
		Use:    "hook-stop",
		Short:  "Stop hook that sends a development run back to commit its work (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := runHookStop(os.Stdin, os.Stderr, dirs); code != 0 {
				return exitCode(code)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&dirs, "dir", nil, "checkout that must be clean when the run ends (repeatable)")
	return cmd
}

// runHookStop keeps a development run from ending while a checkout has
// uncommitted changes: exit 2 sends Claude back with the reason, so it
// commits them itself, the way the repository's conventions say. It asks
// once (stop_hook_active is set the second time) and never blocks on its
// own errors; publish commits whatever is left.
func runHookStop(in io.Reader, errw io.Writer, dirs []string) int {
	var input struct {
		StopHookActive bool `json:"stop_hook_active"`
	}
	data, _ := io.ReadAll(io.LimitReader(in, 1<<20))
	_ = json.Unmarshal(data, &input)
	if input.StopHookActive {
		return 0
	}
	var dirty []string
	for _, d := range dirs {
		out, err := exec.Command("git", "-C", d, "status", "--porcelain").Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			dirty = append(dirty, d)
		}
	}
	if len(dirty) == 0 {
		return 0
	}
	fmt.Fprintf(errw, "Uncommitted changes are left in %s. Commit them, with messages that follow the repository's commit conventions, or discard what does not belong to the change; then finish again.\n", strings.Join(dirty, ", "))
	return 2
}
