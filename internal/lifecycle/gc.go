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
	"strings"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/registry"
)

// GCReport is what GC released (or, in a dry run, would release): manifest
// labels and "port <n>" for stale port links; the manifests directory entries
// it could not read; and every error on the way, including the manifests it
// refused to release because their claims failed verification.
type GCReport struct {
	Released []string
	Ignored  []registry.Problem
	Errors   []error
}

// GC releases the manifests whose file is owned by user owner, or, with
// owner empty, those whose worktree no longer exists, and removes port links
// no manifest references whose worktree is gone. On a server the caller is
// root, so every manifest's claims are verified against its file owner
// before anything is released; on a laptop only d.User's manifests are
// considered. Caddy is reloaded once at the end when a site file went. dryRun
// reports without acting. GC returns an error only when it cannot list the
// manifests or the port links, or cannot resolve owner; everything else is
// in the report.
func GC(ctx context.Context, d Deps, owner string, dryRun bool) (GCReport, error) {
	if d.Log == nil {
		d.Log = io.Discard
	}
	var rep GCReport
	ownerUID := -1
	if owner != "" {
		uid, err := d.uidOf(owner)
		if err != nil {
			return rep, fmt.Errorf("owner %s: %w", owner, err)
		}
		ownerUID = uid
	}
	all, problems, err := d.Reg.All()
	if err != nil {
		return rep, fmt.Errorf("list manifests: %w", err)
	}
	rep.Ignored = problems
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
		ok, err := releasable(m, ownerUID)
		if err != nil {
			rep.Errors = append(rep.Errors, err)
		}
		if !ok {
			continue
		}
		if reason := verifyClaims(d, m); reason != "" {
			rep.Errors = append(rep.Errors, fmt.Errorf("refusing %s: %s", m.Path(), reason))
			continue
		}
		if dryRun {
			rep.Released = append(rep.Released, m.Label)
			continue
		}
		res, siteRemoved, kept := release(ctx, d, m, false, nil)
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
		if len(kept) > 0 {
			fmt.Fprintf(d.Log, "gc: %s: manifest kept (failed: %s); the next gc retries\n", m.Label, strings.Join(kept, ", "))
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
		if err := caddy.SudoReload(ctx, d.Run, d.Bin, false); err != nil {
			rep.Errors = append(rep.Errors, err)
		}
	}
	return rep, nil
}

// releasable reports whether GC releases m: its file is owned by ownerUID,
// or, with ownerUID negative, its worktree is gone.
func releasable(m *registry.Manifest, ownerUID int) (bool, error) {
	if ownerUID >= 0 {
		return m.OwnerUID() == ownerUID, nil
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
