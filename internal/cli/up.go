package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/pg"
	"github.com/quotedo/hades/internal/rabbit"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/version"
	"github.com/quotedo/hades/internal/wtctx"
)

const upUsage = "usage: hades up [--dry-run] [--skip-setup]"

// up turns the resolved worktree into a running dev environment.
func up(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "print what would be done; change nothing")
	skipSetup := fs.Bool("skip-setup", false, "skip the project's setup commands")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(ctx.Stderr, "up: %v\n%s\n", err, upUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(ctx.Stderr, "up: unexpected arguments %q\n%s\n", fs.Args(), upUsage)
		return 2
	}
	opt := lifecycle.UpOptions{DryRun: *dryRun, SkipSetup: *skipSetup}

	// An interrupt cancels the running step, so steps 1 to 9 still roll back.
	bg, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out, err := runUp(bg, ctx, opt)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: up: %v\n", err)
		return 1
	}
	if out.Manifest == nil { // nothing to do; Up said so
		return 0
	}
	where := "at " + out.URL
	if out.URL == "" {
		where = "(no web port)"
	}
	if opt.DryRun {
		fmt.Fprintf(ctx.Stderr, "hades: dry run: %s would be up %s\n", out.Manifest.Label, where)
	} else {
		fmt.Fprintf(ctx.Stderr, "hades: %s up %s\n", out.Manifest.Label, where)
	}
	return 0
}

func runUp(bg context.Context, ctx *Context, opt lifecycle.UpOptions) (*lifecycle.Outcome, error) {
	wt, err := resolveWorktree(bg, ctx)
	if err != nil {
		return nil, err
	}
	d, err := newDeps(ctx)
	if err != nil {
		return nil, err
	}
	return lifecycle.Up(bg, d, wt, opt)
}

// resolveWorktree finds the worktree the command acts on.
func resolveWorktree(bg context.Context, ctx *Context) (wtctx.Context, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return wtctx.Context{}, err
	}
	g := ctx.Flags
	return wtctx.Resolve(bg, wtctx.Flags{Root: g.Root, Worktree: g.Worktree, Name: g.Name, ADE: g.ADE}, ctx.Getenv, cwd, &execx.Real{})
}

// newDeps builds the lifecycle dependencies from the current user and the
// host config. PG is always set (a host always has Postgres settings, server
// or laptop defaults); Rabbit only when the host has a RabbitMQ block.
func newDeps(ctx *Context) (lifecycle.Deps, error) {
	user, err := lifecycle.CurrentUser()
	if err != nil {
		return lifecycle.Deps{}, fmt.Errorf("current user: %w", err)
	}
	if h := ctx.Getenv("HOME"); h != "" {
		user.Home = h
	}
	host, mode, err := config.Load(ctx.Getenv, user.Home)
	if err != nil {
		return lifecycle.Deps{}, err
	}
	d := lifecycle.Deps{
		Host: host,
		Mode: mode,
		Reg:  registry.New(host),
		Run:  &execx.Real{Prepend: execx.ToolPrepend(user.Home, host.ToolPaths)},
		// The client connects only when a command lists or drops databases.
		PG:      &pg.Client{DSN: pg.DSN(host.Postgres, "postgres")},
		User:    user,
		Version: version.Version,
		Log:     ctx.Stderr,
		Bin:     caddy.BinaryPath(ctx.Getenv),
	}
	if r := host.RabbitMQ; r != nil {
		d.Rabbit = &rabbit.Client{ManagementURL: r.ManagementURL, User: r.User, Password: r.Password, HTTP: &http.Client{Timeout: 30 * time.Second}}
	}
	return d, nil
}
