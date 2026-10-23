// Package registry is the shared on-disk state of a hades host: atomic port
// claims (symlinks) and one TOML manifest per worktree.
package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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
	// ErrRegistryPerms means a shared registry directory lacks a property
	// the ownership model rests on.
	ErrRegistryPerms = errors.New("registry directory has the wrong mode or group")
)

// Registry is the set of directories hades keeps its state in.
type Registry struct {
	PortsDir, SitesDir, ManifestsDir string
	Range                            config.PortRange
	// Shared marks a server registry, provisioned for a group of developers:
	// Check then also verifies the directory modes and group (PermProblems).
	Shared bool
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

// Check verifies that the registry directories exist and, for a shared
// registry, have every property PermProblems checks. It never creates or
// changes anything.
func (r *Registry) Check() error {
	if err := r.Exists(); err != nil {
		return err
	}
	if !r.Shared {
		return nil
	}
	problems, err := r.PermProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrRegistryPerms, strings.Join(problems, "; "))
	}
	return nil
}

// PermProblems lists, for each registry directory, every property of a
// shared registry it lacks: the setgid bit (new entries get the directory's
// group), the sticky bit (nobody removes or renames another user's entries),
// group write, no world write, and this process being in the directory's
// group. Root is exempt from the group membership check.
func (r *Registry) PermProblems() ([]string, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return nil, fmt.Errorf("groups of this process: %w", err)
	}
	member := map[int]bool{os.Getegid(): true}
	for _, g := range groups {
		member[g] = true
	}
	var problems []string
	for _, dir := range r.dirs() {
		fi, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		mode := fi.Mode()
		if mode&os.ModeSetgid == 0 {
			problems = append(problems, dir+": missing the setgid bit")
		}
		if mode&os.ModeSticky == 0 {
			problems = append(problems, dir+": missing the sticky bit")
		}
		if mode&0o020 == 0 {
			problems = append(problems, dir+": not group-writable")
		}
		if mode&0o002 != 0 {
			problems = append(problems, dir+": world-writable")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			problems = append(problems, dir+": cannot read its group")
			continue
		}
		if gid := int(st.Gid); os.Geteuid() != 0 && !member[gid] {
			group := strconv.Itoa(gid)
			if g, err := user.LookupGroupId(group); err == nil {
				group = g.Name
			}
			problems = append(problems, fmt.Sprintf("%s: this process is not a member of group %s", dir, group))
		}
	}
	return problems, nil
}

// Exists verifies that the registry directories exist. It never creates
// anything.
func (r *Registry) Exists() error {
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

// PortLinkOwner returns the uid owning the claim link for port (the link
// itself, not its target).
func (r *Registry) PortLinkOwner(port int) (int, error) {
	path := r.portPath(port)
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	return ownerOf(fi, path)
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
