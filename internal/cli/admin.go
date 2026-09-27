package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/raulsh/thefactory/internal/config"
	"github.com/raulsh/thefactory/internal/doctor"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create thefactory's data directory and default configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(paths.Root, 0o700); err != nil {
				return err
			}
			if _, err := os.Stat(paths.Config()); errors.Is(err, os.ErrNotExist) {
				if err := config.Save(paths, config.Default()); err != nil {
					return err
				}
				fmt.Println("wrote", paths.Config())
			} else {
				fmt.Println("kept", paths.Config())
			}
			if _, err := ensureToken(paths.Token()); err != nil {
				return err
			}
			fmt.Println("data directory:", paths.Root)
			fmt.Println("\nnext: `thefactory doctor`, then `thefactory serve --open`")
			return nil
		},
	}
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that Claude Code, gh, git and slk are installed and signed in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return err
			}
			cfg, err := config.Load(paths)
			if err != nil {
				return err
			}
			checks := doctor.Run(cmd.Context(), cfg, paths, doctor.Options{CheckPort: true})
			marks := map[string]string{doctor.OK: "✓", doctor.Warn: "!", doctor.Fail: "✗"}
			for _, c := range checks {
				fmt.Printf("%s %-15s %s\n", marks[c.Status], c.Name, c.Detail)
				if c.Fix != "" && c.Status != doctor.OK {
					fmt.Printf("  %-15s → %s\n", "", c.Fix)
				}
			}
			if !doctor.Healthy(checks) {
				return exitCode(1)
			}
			return nil
		},
	}
}
