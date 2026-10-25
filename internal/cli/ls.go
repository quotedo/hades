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

	"github.com/quotedo/hades/internal/lifecycle"
	"github.com/quotedo/hades/internal/registry"
)

// ls lists the manifests of the registry.
func ls(ctx *Context, args []string) int {
	if code, ok := parseArgs(ctx, flag.NewFlagSet("ls", flag.ContinueOnError), "usage: hades ls [--json]", args); !ok {
		return code
	}
	d, err := newDeps(ctx)
	if err != nil {
		return ctx.fail("ls", err)
	}
	all, problems, err := allManifests(d)
	if err != nil {
		return ctx.fail("ls", err)
	}
	for _, p := range problems {
		fmt.Fprintf(ctx.Stderr, "hades: ls: %s\n", p)
	}
	if all == nil {
		all = []*registry.Manifest{} // JSON prints [] for an empty registry
	}
	if ctx.Flags.JSON {
		if err := writeJSON(ctx.Stdout, all); err != nil {
			return ctx.fail("ls", err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tPORT\tHOST\tWORKTREE\tAGE")
	now := time.Now()
	for _, m := range all {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.Label, strconv.Itoa(m.Port), m.Host, m.Worktree, age(now, m.CreatedAt))
	}
	if err := tw.Flush(); err != nil {
		return ctx.fail("ls", err)
	}
	return 0
}

// allManifests returns every readable manifest and the entries the registry
// skipped. A registry that was never created is empty.
func allManifests(d lifecycle.Deps) ([]*registry.Manifest, []registry.Problem, error) {
	all, problems, err := d.Reg.All()
	if errors.Is(err, fs.ErrNotExist) {
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
