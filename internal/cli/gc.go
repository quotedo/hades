package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/registry"
)

// gc releases the manifests of vanished worktrees.
func gc(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be released; change nothing")
	if code, ok := parseArgs(ctx, fs, "usage: hades gc [--dry-run]", args); !ok {
		return code
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("gc", err)
	}
	if err := d.Reg.Exists(); errors.Is(err, registry.ErrRegistryMissing) {
		return 0 // nothing was ever set up here
	}
	rep, err := lifecycle.GC(context.Background(), d, *dryRun)
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
