package cli

import (
	"context"
	"flag"

	"github.com/quotedo/hades/internal/lifecycle"
)

// down releases what up set up for the resolved worktree. Skipped and failed
// steps are already on stderr through Deps.Log.
func down(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	strict := fs.Bool("strict", false, "exit 1 when any step fails")
	if code, ok := parseArgs(ctx, fs, "usage: hades down [--strict]", args); !ok {
		return code
	}
	// No signal handler: an interrupt kills hades before the manifest goes,
	// so a re-run of down still finds everything it has to release.
	bg := context.Background()
	wt, err := resolveWorktree(bg, ctx)
	if err != nil {
		return ctx.fail("down", err)
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("down", err)
	}
	if _, err := lifecycle.Down(bg, d, wt, *strict); err != nil {
		return ctx.fail("down", err)
	}
	return 0
}
