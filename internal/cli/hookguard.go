package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/raulsh/thefactory/internal/guard"
)

func newHookGuardCmd() *cobra.Command {
	var stage string
	cmd := &cobra.Command{
		Use:    "hook-guard",
		Short:  "PreToolUse hook that keeps agents from publishing (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := runHookGuard(os.Stdin, os.Stderr); code != 0 {
				return exitCode(code)
			}
			return nil
		},
	}
	// The stage is recorded in the hook command line for readability of run
	// logs; the rules are the same for every stage today.
	cmd.Flags().StringVar(&stage, "stage", "", "run kind the hook guards")
	return cmd
}

// runHookGuard implements the hook protocol: exit 0 allows the tool call,
// exit 2 blocks it and feeds stderr back to the agent. It fails closed: any
// problem reading the input, or a panic, blocks the call.
func runHookGuard(in io.Reader, errw io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(errw, "thefactory guard failed (%v); the command was blocked\n", r)
			code = 2
		}
	}()
	data, err := io.ReadAll(io.LimitReader(in, 8<<20))
	if err != nil {
		fmt.Fprintln(errw, "thefactory guard could not read the hook input; the command was blocked")
		return 2
	}
	var input guard.Input
	if err := json.Unmarshal(data, &input); err != nil {
		fmt.Fprintln(errw, "thefactory guard could not parse the hook input; the command was blocked")
		return 2
	}
	d := guard.Check(input)
	if d.Allow {
		return 0
	}
	fmt.Fprintln(errw, d.Reason)
	return 2
}
