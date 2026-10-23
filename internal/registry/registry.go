// Package registry is the shared on-disk state of a hades host: atomic port
// claims (symlinks) and one TOML manifest per worktree.
package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/quotedo/hades/internal/config"
)

var (
	// ErrRegistryMissing means a registry directory does not exist.
	ErrRegistryMissing = errors.New("registry directory missing")
	// ErrNoFreePort means every port in the range is claimed.
	ErrNoFreePort = errors.New("no free port in range")
	// ErrNotOwner means the port is claimed by a different worktree.
	ErrNotOwner = errors.New("port is not owned by this worktree")
	// ErrNoManifest means no manifest matches the query.
	ErrNoManifest = errors.New("no manifest found")
)

// Registry is the set of directories hades keeps its state in.
type Registry struct {
	PortsDir, SitesDir, ManifestsDir string
	Range                            config.PortRange
}

// New builds a Registry from the host configuration.
func New(h *config.Host) *Registry {
	return &Registry{
		PortsDir:     h.PortsDir,
		SitesDir:     h.SitesDir,
		ManifestsDir: h.ManifestsDir,
		Range:        h.Range(),
	}
}

func (r *Registry) dirs() []string {
	return []string{r.PortsDir, r.SitesDir, r.ManifestsDir}
}

// EnsureDirs creates the registry directories with the given mode (laptop mode).
func (r *Registry) EnsureDirs(mode os.FileMode) error {
	for _, d := range r.dirs() {
		if err := os.MkdirAll(d, mode); err != nil {
			return err
		}
	}
	return nil
}

// Check verifies that the registry directories exist (server mode, where the
// provisioning playbook owns them). It never creates anything.
func (r *Registry) Check() error {
	for _, d := range r.dirs() {
		fi, err := os.Stat(d)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s", ErrRegistryMissing, d)
			}
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("%w: %s is not a directory", ErrRegistryMissing, d)
		}
	}
	return nil
}

func (r *Registry) portPath(port int) string {
	return filepath.Join(r.PortsDir, strconv.Itoa(port))
}

// ClaimPort atomically claims the lowest free port in the range for worktree.
func (r *Registry) ClaimPort(worktree string) (int, error) {
	for p := r.Range.Lo; p <= r.Range.Hi; p++ {
		err := os.Symlink(worktree, r.portPath(p))
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return 0, err
		}
	}
	return 0, ErrNoFreePort
}

// PortOwner returns the worktree that claimed port.
func (r *Registry) PortOwner(port int) (string, error) {
	return os.Readlink(r.portPath(port))
}

// ReleasePort removes the claim on port, only if worktree owns it.
func (r *Registry) ReleasePort(port int, worktree string) error {
	owner, err := r.PortOwner(port)
	if err != nil {
		return err
	}
	if owner != worktree {
		return fmt.Errorf("%w: port %d is claimed by %s", ErrNotOwner, port, owner)
	}
	return os.Remove(r.portPath(port))
}
