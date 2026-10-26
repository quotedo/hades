package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/pg"
)

// probeTimeout bounds each network probe of status and doctor.
const probeTimeout = 3 * time.Second

// pgProbe lists the databases to test Postgres; tests replace it.
var pgProbe = func(ctx context.Context, db pg.Databases) ([]string, error) { return db.List(ctx) }

type statusReport struct {
	HostConfig string      `json:"host_config"`
	Domain     string      `json:"domain"`
	Ports      statusPorts `json:"ports"`
	Worktrees  int         `json:"worktrees"`
	Postgres   statusPG    `json:"postgres"`
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

// status summarises the host: host config, ports, worktrees and Postgres.
func status(ctx *Context, args []string) int {
	if code, ok := parseArgs(ctx, flag.NewFlagSet("status", flag.ContinueOnError), "usage: hades status [--json]", args); !ok {
		return code
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("status", err)
	}
	r, err := buildStatus(context.Background(), d)
	if err != nil {
		return ctx.fail("status", err)
	}
	if ctx.Flags.JSON {
		if err := writeJSON(ctx.Stdout, r); err != nil {
			return ctx.fail("status", err)
		}
		return 0
	}
	fmt.Fprintf(ctx.Stdout, "host_config: %s\ndomain: %s\n", r.HostConfig, r.Domain)
	fmt.Fprintf(ctx.Stdout, "ports.range: %s\nports.used: %d\nports.free: %d\n", r.Ports.Range, r.Ports.Used, r.Ports.Free)
	fmt.Fprintf(ctx.Stdout, "worktrees: %d\npostgres.reachable: %t\npostgres.databases: %d\n",
		r.Worktrees, r.Postgres.Reachable, r.Postgres.Databases)
	return 0
}

func buildStatus(bg context.Context, d lifecycle.Deps) (statusReport, error) {
	r := statusReport{HostConfig: d.Host.Path, Domain: d.Host.Domain}
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
	return r, nil
}

// usedPorts counts the port claims (symlinks) in the ports directory. A
// registry that was never created has none.
func usedPorts(d lifecycle.Deps) (int, error) {
	entries, err := os.ReadDir(d.Reg.PortsDir)
	if errors.Is(err, fs.ErrNotExist) {
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
