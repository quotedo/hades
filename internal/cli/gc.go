package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/registry"
)

var errRootOnServer = errors.New("must run as root on a server")

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
	// Checked before loading the host config, which a non-root user may not
	// be allowed to read, and again after, in case the file appeared since.
	if serverHost(ctx.Getenv) && geteuid() != 0 {
		return ctx.fail("gc", errRootOnServer)
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("gc", err)
	}
	if d.Mode == config.Server && geteuid() != 0 {
		return ctx.fail("gc", errRootOnServer)
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

// serverHost reports whether config.Load would use a server host config,
// without reading it: the file exists (or cannot even be checked) and laptop
// mode is not forced. A missing explicit path is left to config.Load to report.
func serverHost(getenv func(string) string) bool {
	if getenv("HADES_HOST_CONFIG") == config.LaptopOverride {
		return false
	}
	_, err := os.Stat(config.HostPath(getenv))
	return !errors.Is(err, fs.ErrNotExist)
}
