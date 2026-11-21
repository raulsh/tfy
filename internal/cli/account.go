package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/raulsh/tfy/internal/config"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
)

func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage the accounts runs sign in with",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "secret <id>",
		Short: "Set an account's token or key, read from stdin",
		Long: `Set the secret of an account added in Settings: a subscription token from
` + "`claude setup-token`" + `, an API key, or a Bedrock API key. It is read from stdin, so
it never crosses the network to the web UI, and never shows when typed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !domain.ValidAccountID(id) {
				return fmt.Errorf("%q is not an account id", id)
			}
			paths, err := config.DefaultPaths()
			if err != nil {
				return err
			}
			st, err := store.Open(cmd.Context(), paths.DB())
			if err != nil {
				return err
			}
			defer st.Close()
			if _, err := st.Q.GetAccount(cmd.Context(), id); err != nil {
				if store.IsNotFound(err) {
					return fmt.Errorf("there is no account %q: add it in Settings first", id)
				}
				return err
			}
			secret, err := readSecret(os.Stdin)
			if err != nil {
				return err
			}
			if secret == "" {
				return fmt.Errorf("no secret given")
			}
			if err := config.WriteAccountSecret(paths, id, secret); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "saved the secret of", id)
			return nil
		},
	})
	return cmd
}

// readSecret reads one line from stdin, without echo when it is a terminal.
func readSecret(f *os.File) (string, error) {
	if term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(os.Stderr, "secret (hidden): ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
