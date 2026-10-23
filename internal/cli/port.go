package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/quotedo/hades/internal/registry"
)

// port prints the web port of the current worktree. It is what hades-port
// runs, so it prints nothing else on stdout.
func port(ctx *Context, args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(ctx.Stderr, "port: unexpected arguments %q\nusage: hades port\n", args)
		return 2
	}
	bg := context.Background()
	wt, err := resolveWorktree(bg, ctx)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: port: %v\n", err)
		return 1
	}
	d, err := newDeps(ctx)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: port: %v\n", err)
		return 1
	}
	m, err := d.Reg.ByWorktree(wt.Worktree)
	if errors.Is(err, registry.ErrNoManifest) || errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(ctx.Stderr, "hades: port: no hades manifest for %s; run hades up\n", wt.Worktree)
		return 1
	}
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: port: %v\n", err)
		return 1
	}
	if m.Port == 0 {
		fmt.Fprintf(ctx.Stderr, "hades: port: %s has no web port\n", m.Label)
		return 1
	}
	fmt.Fprintf(ctx.Stdout, "%d\n", m.Port)
	return 0
}
