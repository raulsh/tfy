// Package cli holds thefactory's commands.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time with -ldflags "-X .../internal/cli.version=…".
var version = "dev"

// exitCode is an error that carries a process exit code and has already been
// reported, or needs no message.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// Execute runs the root command and returns the process exit code.
func Execute() int {
	root := newRoot()
	err := root.Execute()
	if err == nil {
		return 0
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "thefactory",
		Short:         "From feedback to merged pull requests, orchestrated through Claude Code",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newServeCmd(),
		newInitCmd(),
		newDoctorCmd(),
		newHookGuardCmd(),
		newDevCmd(),
	)
	return root
}
