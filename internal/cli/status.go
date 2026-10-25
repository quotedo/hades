package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/pg"
)

// probeTimeout bounds each network probe of status and doctor.
const probeTimeout = 3 * time.Second

// pgProbe lists the databases to test Postgres; tests replace it.
var pgProbe = func(ctx context.Context, db pg.Databases) ([]string, error) { return db.List(ctx) }

type statusReport struct {
	Mode       string      `json:"mode"`
	HostConfig string      `json:"host_config"`
	Domain     string      `json:"domain"`
	Ports      statusPorts `json:"ports"`
	Worktrees  int         `json:"worktrees"`
	Postgres   statusPG    `json:"postgres"`
	CaddyCheck string      `json:"caddy_check"`
}

type statusPorts struct {
	Range string `json:"range"`
	Used  int    `json:"used"`
	Free  int    `json:"free"`
}

type statusPG struct {
	Reachable bool `json:"reachable"`
	Databases int  `json:"databases"`
}

// status summarises the host: mode, ports, worktrees, Postgres and Caddy.
func status(ctx *Context, args []string) int {
	if !parseArgs(ctx, flag.NewFlagSet("status", flag.ContinueOnError), "usage: hades status [--json]", args) {
		return 2
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("status", err)
	}
	r, err := buildStatus(context.Background(), ctx, d)
	if err != nil {
		return ctx.fail("status", err)
	}
	if ctx.Flags.JSON {
		if err := writeJSON(ctx.Stdout, r); err != nil {
			return ctx.fail("status", err)
		}
		return 0
	}
	fmt.Fprintf(ctx.Stdout, "mode: %s\nhost_config: %s\ndomain: %s\n", r.Mode, r.HostConfig, r.Domain)
	fmt.Fprintf(ctx.Stdout, "ports.range: %s\nports.used: %d\nports.free: %d\n", r.Ports.Range, r.Ports.Used, r.Ports.Free)
	fmt.Fprintf(ctx.Stdout, "worktrees: %d\npostgres.reachable: %t\npostgres.databases: %d\ncaddy_check: %s\n",
		r.Worktrees, r.Postgres.Reachable, r.Postgres.Databases, r.CaddyCheck)
	return 0
}

func buildStatus(bg context.Context, ctx *Context, d lifecycle.Deps) (statusReport, error) {
	r := statusReport{Mode: "laptop", HostConfig: "laptop", Domain: d.Host.Domain, CaddyCheck: "n/a"}
	if d.Mode == config.Server {
		r.Mode = "server"
		r.HostConfig = config.HostPath(ctx.Getenv)
	}
	used, err := usedPorts(d)
	if err != nil {
		return r, err
	}
	rng := d.Host.Ports
	r.Ports = statusPorts{Range: d.Host.PortRange, Used: used, Free: max(rng.Hi-rng.Lo+1-used, 0)}

	all, _, err := allManifests(d)
	if err != nil {
		return r, err
	}
	r.Worktrees = len(all)

	pgCtx, cancel := context.WithTimeout(bg, probeTimeout)
	defer cancel()
	if dbs, err := pgProbe(pgCtx, d.PG); err == nil {
		r.Postgres = statusPG{Reachable: true, Databases: len(dbs)}
	}

	if d.Mode == config.Server {
		r.CaddyCheck = "ok"
		if err := caddy.SudoReload(bg, d.Run, d.Bin, true); err != nil {
			r.CaddyCheck = "error: " + err.Error()
		}
	}
	return r, nil
}

// usedPorts counts the port claims (symlinks) in the ports directory. On a
// laptop a registry that was never created has none.
func usedPorts(d lifecycle.Deps) (int, error) {
	entries, err := os.ReadDir(d.Reg.PortsDir)
	if errors.Is(err, fs.ErrNotExist) && d.Mode == config.Laptop {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			n++
		}
	}
	return n, nil
}
