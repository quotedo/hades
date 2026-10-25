package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/registry"
)

// geteuid is os.Geteuid; tests replace it.
var geteuid = os.Geteuid

// gc releases the manifests of vanished worktrees, or of one user. On a
// server it must run as root; on a laptop it acts for the current user only.
func gc(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be released; change nothing")
	owner := fs.String("owner", "", "release every manifest whose file this user owns")
	if !parseArgs(ctx, fs, "usage: hades gc [--dry-run] [--owner USER]", args) {
		return 2
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("gc", err)
	}
	if d.Mode == config.Server && geteuid() != 0 {
		return ctx.fail("gc", errors.New("must run as root on a server"))
	}
	if d.Mode == config.Laptop {
		if err := d.Reg.Check(); errors.Is(err, registry.ErrRegistryMissing) {
			return 0 // nothing was ever set up here
		}
	}
	rep, err := lifecycle.GC(context.Background(), d, *owner, *dryRun)
	if err != nil {
		return ctx.fail("gc", err)
	}
	for _, p := range rep.Ignored {
		fmt.Fprintf(ctx.Stderr, "hades: gc: %s\n", p)
	}
	verb := "released"
	if *dryRun {
		verb = "would release"
	}
	for _, label := range rep.Released {
		fmt.Fprintf(ctx.Stderr, "hades: gc: %s %s\n", verb, label)
	}
	for _, err := range rep.Errors {
		fmt.Fprintf(ctx.Stderr, "hades: gc: %v\n", err)
	}
	if len(rep.Errors) > 0 {
		return 1
	}
	return 0
}
