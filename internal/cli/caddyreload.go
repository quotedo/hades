package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
)

// rootHostConfig is the only host config a root caddy-reload reads.
const rootHostConfig = "/etc/hades/host.toml"

// loadReloadConfig decides which host config caddy-reload reads. As root
// (reached through sudo) the environment is attacker-influenced, so
// HADES_HOST_CONFIG and HOME are ignored and the fixed path is used, but only
// if it is a regular file owned by root and not group or world writable.
// Otherwise the normal config.Load rules apply. path is the root-mode file.
func loadReloadConfig(euid int, getenv func(string) string, path string) (*config.Host, config.Mode, error) {
	if euid != 0 {
		h, m, err := config.Load(getenv, getenv("HOME"))
		return h, m, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, config.Server, fmt.Errorf("host config %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, config.Server, fmt.Errorf("host config %s is not a regular file", path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
		return nil, config.Server, fmt.Errorf("host config %s is not owned by root", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return nil, config.Server, fmt.Errorf("host config %s is group or world writable", path)
	}
	h, err := config.LoadHost(path)
	return h, config.Server, err
}

// caddyReload validates the site files and reloads Caddy. It runs as root
// through sudo, so it takes no input beyond --check and the host config.
func caddyReload(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("caddy-reload", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "validate only; do not reload")
	// usage() would make the command table depend on itself, so report here.
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(ctx.Stderr, "caddy-reload: %v\nusage: hades caddy-reload [--check]\n", err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(ctx.Stderr, "caddy-reload: unexpected arguments %q\nusage: hades caddy-reload [--check]\n", fs.Args())
		return 2
	}
	host, mode, err := loadReloadConfig(os.Geteuid(), ctx.Getenv, rootHostConfig)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "caddy-reload: %v\n", err)
		return 1
	}
	if mode == config.Laptop {
		fmt.Fprintln(ctx.Stderr, "caddy-reload: no host config; nothing to reload")
		return 1
	}
	// As root the commands search only caddy's fixed PATH. A non-root run
	// escalates nothing, so it finds caddy and the reload command on the
	// caller's PATH first, as every other hades command does.
	run := &execx.Real{}
	if os.Geteuid() != 0 {
		run.Prepend = filepath.SplitList(ctx.Getenv("PATH"))
	}
	if err := caddy.Reload(context.Background(), host, *check, run); err != nil {
		fmt.Fprintf(ctx.Stderr, "caddy-reload: %v\n", err)
		return 1
	}
	return 0
}
