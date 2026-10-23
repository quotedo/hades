package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/quotedo/hades/internal/lifecycle"
)

const downUsage = "usage: hades down [--strict]"

// down releases what up set up for the resolved worktree. Skipped and failed
// steps are already on stderr through Deps.Log.
func down(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	strict := fs.Bool("strict", false, "exit 1 when any step fails")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(ctx.Stderr, "down: %v\n%s\n", err, downUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(ctx.Stderr, "down: unexpected arguments %q\n%s\n", fs.Args(), downUsage)
		return 2
	}
	// No signal handler: an interrupt kills hades before the manifest goes,
	// so a re-run of down still finds everything it has to release.
	bg := context.Background()
	wt, err := resolveWorktree(bg, ctx)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: down: %v\n", err)
		return 1
	}
	d, err := newDeps(ctx)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: down: %v\n", err)
		return 1
	}
	if _, err := lifecycle.Down(bg, d, wt, *strict); err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: down: %v\n", err)
		return 1
	}
	return 0
}
