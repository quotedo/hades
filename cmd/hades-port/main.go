// Command hades-port prints the port for a worktree. Paseo passes four
// positional arguments, which are ignored.
package main

import (
	"os"

	"github.com/quotedo/hades/internal/cli"
)

func main() {
	os.Exit(cli.Main([]string{"port"}, os.Stdout, os.Stderr, os.Getenv))
}
