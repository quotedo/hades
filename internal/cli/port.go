package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"

	"github.com/quotedo/hades/internal/registry"
)

// port prints the web port of the current worktree. It is what hades-port
// runs, so it prints nothing else on stdout.
func port(ctx *Context, args []string) int {
	if !parseArgs(ctx, flag.NewFlagSet("port", flag.ContinueOnError), "usage: hades port", args) {
		return 2
	}
	bg := context.Background()
	wt, err := resolveWorktree(bg, ctx)
	if err != nil {
		return ctx.fail("port", err)
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("port", err)
	}
	m, err := d.Reg.ByWorktree(wt.Worktree, d.User.UID)
	if errors.Is(err, registry.ErrNoManifest) || errors.Is(err, fs.ErrNotExist) {
		return ctx.fail("port", fmt.Errorf("no hades manifest for %s; run hades up", wt.Worktree))
	}
	if err != nil {
		return ctx.fail("port", err)
	}
	if m.Port == 0 {
		return ctx.fail("port", fmt.Errorf("%s has no web port", m.Label))
	}
	fmt.Fprintf(ctx.Stdout, "%d\n", m.Port)
	return 0
}
