package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/quotedo/hades/internal/naming"
)

// Manifest records everything hades set up for one worktree. The registry is
// shared by every developer of a host, so a manifest read from disk is
// untrusted input: User and UID are informational, and decisions use the
// owner of the file it was read from (OwnerUID).
type Manifest struct {
	HadesVersion string    `toml:"hades_version"`
	CreatedAt    time.Time `toml:"created_at"`
	User         string    `toml:"user"`
	UID          int       `toml:"uid"`
	Project      string    `toml:"project"`
	Name         string    `toml:"name"`
	Label        string    `toml:"label"`
	Host         string    `toml:"host"`
	Port         int       `toml:"port"`
	Worktree     string    `toml:"worktree"`
	Root         string    `toml:"root"`
	Preset       string    `toml:"preset"`
	DBSuffix     string    `toml:"db_suffix"`
	DBBases      []string  `toml:"db_bases"` // database names minus DBSuffix
	Vhost        string    `toml:"vhost"`
	ADE          string    `toml:"ade"`

	// Set when read from disk, never encoded.
	path     string
	ownerUID int
	fromDisk bool
}

// OwnerUID is the uid owning the file m was read from, or -1 for a manifest
// that was not read from disk.
func (m *Manifest) OwnerUID() int {
	if !m.fromDisk {
		return -1
	}
	return m.ownerUID
}

// Path is the file m was read from; empty for a manifest not read from disk.
func (m *Manifest) Path() string { return m.path }

// Problem is a file in the manifests directory that All skipped, and why.
type Problem struct {
	Path, Reason string
}

func (p Problem) String() string { return "ignored manifest " + p.Path + ": " + p.Reason }

// ManifestPath returns the manifest file for label, refusing a label that is
// not a plain file name.
func (r *Registry) ManifestPath(label string) (string, error) {
	if err := naming.CheckPlain(label); err != nil {
		return "", err
	}
	return filepath.Join(r.ManifestsDir, label+".toml"), nil
}

// WriteManifest atomically writes m to ManifestsDir/<label>.toml (mode 0644).
// Rewriting a manifest read from disk keeps its file owner, so a manifest
// root rewrites (gc) stays its owner's.
func (r *Registry) WriteManifest(m *Manifest) error {
	path, err := r.ManifestPath(m.Label)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.ManifestsDir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	c := *m
	c.CreatedAt = m.CreatedAt.UTC()
	if err := toml.NewEncoder(tmp).Encode(&c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if m.fromDisk && m.ownerUID != os.Geteuid() {
		if err := tmp.Chown(m.ownerUID, -1); err != nil {
			tmp.Close()
			return fmt.Errorf("keep owner uid %d of %s: %w", m.ownerUID, path, err)
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// errNotRegular marks a manifest path that is not a regular file.
var errNotRegular = errors.New("not a regular file")

// readManifest reads the regular file at path without following a symlink
// and records the file's owner. A missing file is fs.ErrNotExist.
func readManifest(path string) (*Manifest, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("manifest %s: %w", path, errNotRegular)
	}
	// O_NOFOLLOW refuses a symlink swapped in since the Lstat; O_NONBLOCK
	// keeps a FIFO swapped in from blocking the open.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("manifest %s: %w", path, errNotRegular)
	}
	uid, err := ownerOf(fi, path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if _, err := toml.NewDecoder(f).Decode(&m); err != nil {
		return nil, &parseError{path: path, err: err}
	}
	m.path, m.ownerUID, m.fromDisk = path, uid, true
	return &m, nil
}

// parseError is a manifest that is not valid TOML for a Manifest.
type parseError struct {
	path string
	err  error
}

func (e *parseError) Error() string { return "parse manifest " + e.path + ": " + e.err.Error() }

func (e *parseError) Unwrap() error { return e.err }

// ByLabel loads the manifest for label; fs.ErrNotExist when absent. A
// manifest recording another label than its file name is an error.
func (r *Registry) ByLabel(label string) (*Manifest, error) {
	path, err := r.ManifestPath(label)
	if err != nil {
		return nil, err
	}
	m, err := readManifest(path)
	if err != nil {
		return nil, err
	}
	if m.Label != label {
		return nil, fmt.Errorf("manifest %s records label %q, not its file name", path, m.Label)
	}
	return m, nil
}

// All returns every readable manifest sorted by label, and the entries it
// skipped: anything that is not a regular file, records a label that is not
// a plain name or not its own file name, fails to parse, or vanished between
// the listing and the read. The error is only for failing to list the
// directory.
func (r *Registry) All() ([]*Manifest, []Problem, error) {
	entries, err := os.ReadDir(r.ManifestsDir)
	if err != nil {
		return nil, nil, err
	}
	var out []*Manifest
	var problems []Problem
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok {
			continue
		}
		path := filepath.Join(r.ManifestsDir, e.Name())
		skip := func(reason string) {
			problems = append(problems, Problem{Path: path, Reason: reason})
		}
		if !e.Type().IsRegular() {
			skip("not a regular file")
			continue
		}
		m, err := readManifest(path)
		var pe *parseError
		switch {
		case errors.Is(err, fs.ErrNotExist):
			skip("removed while listing")
			continue
		case errors.Is(err, errNotRegular):
			skip("not a regular file")
			continue
		case errors.As(err, &pe):
			skip(pe.err.Error())
			continue
		case err != nil:
			skip(err.Error())
			continue
		}
		if err := naming.CheckPlain(m.Label); err != nil {
			skip(err.Error())
			continue
		}
		if m.Label != stem {
			skip(fmt.Sprintf("records label %q, not its file name", m.Label))
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, problems, nil
}

// canonical resolves symlinks when the path exists, else just cleans it.
func canonical(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// ByWorktree finds the manifest owned by uid whose worktree is path (symlinks
// resolved); uid 0 considers every owner. A manifest of another user naming
// this path is not a match, so nobody can shadow another user's worktree.
// Unreadable entries are skipped as in All.
func (r *Registry) ByWorktree(path string, uid int) (*Manifest, error) {
	all, _, err := r.All()
	if err != nil {
		return nil, err
	}
	want := canonical(path)
	for _, m := range all {
		if uid != 0 && m.OwnerUID() != uid {
			continue
		}
		if canonical(m.Worktree) == want {
			return m, nil
		}
	}
	return nil, fmt.Errorf("%w for worktree %s", ErrNoManifest, path)
}

// RemoveManifest deletes the manifest for label.
func (r *Registry) RemoveManifest(label string) error {
	path, err := r.ManifestPath(label)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// ManifestOwner returns the uid owning the manifest file for label, which
// must be a regular file. Ownership decisions use this (or
// Manifest.OwnerUID), never the UID recorded inside the manifest.
func (r *Registry) ManifestOwner(label string) (int, error) {
	path, err := r.ManifestPath(label)
	if err != nil {
		return 0, err
	}
	return RegularFileOwner(path)
}

// RegularFileOwner returns the owner uid of the regular file at path, without
// following a symlink.
func RegularFileOwner(path string) (int, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("%s: %w", path, errNotRegular)
	}
	return ownerOf(fi, path)
}

func ownerOf(fi fs.FileInfo, path string) (int, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no owner information on this platform", path)
	}
	return int(st.Uid), nil
}
