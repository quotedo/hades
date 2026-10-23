package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/registry"
)

const lsUsage = "usage: hades ls [--mine] [--json]"

// ls lists the manifests of the host registry.
func ls(ctx *Context, args []string) int {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mine := fs.Bool("mine", false, "only the current user's worktrees")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(ctx.Stderr, "ls: %v\n%s\n", err, lsUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(ctx.Stderr, "ls: unexpected arguments %q\n%s\n", fs.Args(), lsUsage)
		return 2
	}
	d, err := newDeps(ctx)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: ls: %v\n", err)
		return 1
	}
	all, problems, err := allManifests(d)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: ls: %v\n", err)
		return 1
	}
	for _, p := range problems {
		fmt.Fprintf(ctx.Stderr, "hades: ls: %s\n", p)
	}
	list := []registry.Manifest{} // never nil: JSON prints [] for an empty registry
	for _, m := range all {
		if *mine && m.User != d.User.Name {
			continue
		}
		list = append(list, *m)
	}
	if ctx.Flags.JSON {
		if err := writeJSON(ctx.Stdout, list); err != nil {
			fmt.Fprintf(ctx.Stderr, "hades: ls: %v\n", err)
			return 1
		}
		return 0
	}
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tUSER\tPORT\tHOST\tWORKTREE\tAGE")
	now := time.Now()
	for _, m := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Label, m.User, strconv.Itoa(m.Port), m.Host, m.Worktree, age(now, m.CreatedAt))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: ls: %v\n", err)
		return 1
	}
	return 0
}

// allManifests returns every readable manifest and the entries the registry
// skipped. On a laptop a registry that was never created is empty; on a
// server a missing directory is an error.
func allManifests(d lifecycle.Deps) ([]*registry.Manifest, []registry.Problem, error) {
	all, problems, err := d.Reg.All()
	if err != nil && d.Mode == config.Laptop && errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	return all, problems, err
}

func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// age renders the time since t as minutes, hours or days; "-" when unknown.
func age(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	default:
		return strconv.Itoa(max(int(d/time.Minute), 0)) + "m"
	}
}
