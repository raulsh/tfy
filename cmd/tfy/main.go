// Command tfy takes units of work from feedback to merged pull
// requests by orchestrating Claude Code.
package main

import (
	"os"

	"github.com/raulsh/tfy/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
