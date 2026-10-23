package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/registry"
)

// GCReport is what GC released (or, in a dry run, would release): manifest
// labels and "port <n>" for stale port links, and every error on the way.
type GCReport struct {
	Released []string
	Errors   []error
}

// GC releases the manifests of user owner, or, with owner empty, those whose
// worktree no longer exists, and removes port links no manifest references
// whose worktree is gone. It does not check ownership: on a server the
// caller is root, and on a laptop only d.User's manifests are considered.
// Caddy is reloaded once at the end when a site file went. dryRun reports
// without acting. GC returns an error only when it cannot list the manifests
// or the port links; everything else is in the report.
func GC(ctx context.Context, d Deps, owner string, dryRun bool) (GCReport, error) {
	if d.Log == nil {
		d.Log = io.Discard
	}
	var rep GCReport
	all, err := d.Reg.All()
	if err != nil {
		return rep, fmt.Errorf("list manifests: %w", err)
	}
	links, err := os.ReadDir(d.Reg.PortsDir)
	if err != nil {
		return rep, fmt.Errorf("list ports: %w", err)
	}

	referenced := map[int]bool{}
	reload := false
	for _, m := range all {
		if m.Port != 0 {
			referenced[m.Port] = true
		}
		if d.Mode == config.Laptop && m.User != d.User.Name {
			continue
		}
		ok, err := releasable(m, owner)
		if err != nil {
			rep.Errors = append(rep.Errors, err)
		}
		if !ok {
			continue
		}
		if dryRun {
			rep.Released = append(rep.Released, m.Label)
			continue
		}
		res, siteRemoved := release(ctx, d, m, false)
		reload = reload || siteRemoved
		removed := false
		for _, r := range res {
			switch {
			case r.Skipped:
				fmt.Fprintf(d.Log, "gc: %s: skipped %s: %s\n", m.Label, r.Step, r.Reason)
			case r.Err != nil:
				rep.Errors = append(rep.Errors, fmt.Errorf("%s: %s: %w", m.Label, r.Step, r.Err))
			case r.Step == "manifest":
				removed = true
			}
		}
		if removed {
			rep.Released = append(rep.Released, m.Label)
		}
	}

	for _, e := range links {
		port, err := strconv.Atoi(e.Name())
		if err != nil || referenced[port] {
			continue // not a port link, or released with its manifest
		}
		released, err := releaseStaleLink(d.Reg, port, dryRun)
		if err != nil {
			rep.Errors = append(rep.Errors, err)
		}
		if released {
			rep.Released = append(rep.Released, fmt.Sprintf("port %d", port))
		}
	}

	if reload {
		if err := caddy.SudoReload(ctx, d.Run, d.Bin); err != nil {
			rep.Errors = append(rep.Errors, err)
		}
	}
	return rep, nil
}

// releasable reports whether GC releases m: it belongs to owner, or, with
// owner empty, its worktree is gone.
func releasable(m *registry.Manifest, owner string) (bool, error) {
	if owner != "" {
		return m.User == owner, nil
	}
	_, err := os.Stat(m.Worktree)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("%s: %w", m.Label, err)
	}
	return false, nil
}

// releaseStaleLink removes the link for port when the directory it points to
// is gone.
func releaseStaleLink(reg *registry.Registry, port int, dryRun bool) (bool, error) {
	target, err := reg.PortOwner(port)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // released since the listing
	}
	if err != nil {
		return false, fmt.Errorf("port %d: %w", port, err)
	}
	// Stat follows the link, so a relative target resolves as the link does.
	_, err = os.Stat(filepath.Join(reg.PortsDir, strconv.Itoa(port)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, fmt.Errorf("port %d: %w", port, err)
	default:
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := reg.ReleasePort(port, target); err != nil {
		return false, fmt.Errorf("port %d: %w", port, err)
	}
	return true, nil
}
