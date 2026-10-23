package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
)

// caddyReload validates the site files and reloads Caddy. It runs as root
// through sudo, so it takes no input beyond --check and the host config.
func caddyReload(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("caddy-reload", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "validate only; do not reload")
	if err := fs.Parse(args); err != nil {
		// usage() would make the command table depend on itself.
		fmt.Fprintf(ctx.Stderr, "caddy-reload: %v\n", err)
		return 2
	}
	host, mode, err := config.Load(ctx.Getenv, ctx.Getenv("HOME"))
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "caddy-reload: %v\n", err)
		return 1
	}
	if mode == config.Laptop {
		fmt.Fprintln(ctx.Stderr, "caddy-reload: no host config; nothing to reload")
		return 1
	}
	if err := caddy.Reload(context.Background(), host.Caddy, host.SitesDir, *check, &execx.Real{}); err != nil {
		fmt.Fprintf(ctx.Stderr, "caddy-reload: %v\n", err)
		return 1
	}
	return 0
}
