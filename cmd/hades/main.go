// Command hades sets up and tears down git worktrees for ADE hooks.
package main

import (
	"os"

	"github.com/quotedo/hades/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
