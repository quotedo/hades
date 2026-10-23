package lifecycle

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/registry"
)

// verifyClaims checks what m records against what the file system guarantees
// about it, before root releases a manifest that may not be its own (gc, and
// down as root). The registry is writable by every developer of the host, so
// a manifest is believed only as far as its file owner vouches for it: the
// label is its file name, the vhost is empty or the label, the database
// suffix is the one hades computes for the owner's login and the recorded
// name, and a port link claiming this worktree belongs to the owner. An owner
// uid without a login is refused. It returns why m must be left alone, or ""
// when its claims hold.
func verifyClaims(d Deps, m *registry.Manifest) string {
	owner := m.OwnerUID()
	if owner < 0 {
		return "not read from the registry"
	}
	if err := naming.CheckPlain(m.Label); err != nil {
		return err.Error()
	}
	if filepath.Base(m.Path()) != m.Label+".toml" {
		return fmt.Sprintf("records label %q, not its file name", m.Label)
	}
	if m.Vhost != "" && m.Vhost != m.Label {
		return fmt.Sprintf("vhost %q is not its label %q", m.Vhost, m.Label)
	}
	// Never fall back to the recorded user field: the file's author wrote it.
	login, err := d.loginOf(owner)
	if err != nil {
		return fmt.Sprintf("cannot resolve owner uid %d: %v", owner, err)
	}
	if want := naming.Suffix(login, naming.Sanitize(m.Name)); m.DBSuffix != want {
		return fmt.Sprintf("db_suffix %q is not %q, the suffix of owner %s for name %q", m.DBSuffix, want, login, m.Name)
	}
	if m.Port != 0 {
		// A link that is gone or points at another worktree is nothing to
		// release (the port step skips it); only a link claiming this
		// worktree must belong to the manifest's owner.
		target, err := d.Reg.PortOwner(m.Port)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return fmt.Sprintf("port %d: %v", m.Port, err)
		case target == m.Worktree:
			uid, err := d.Reg.PortLinkOwner(m.Port)
			switch {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				return fmt.Sprintf("port %d: %v", m.Port, err)
			case uid != owner:
				return fmt.Sprintf("port link %d is owned by uid %d, the manifest by uid %d", m.Port, uid, owner)
			}
		}
	}
	return ""
}
