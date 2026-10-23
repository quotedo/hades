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
)

// Manifest records everything hades set up for one worktree.
type Manifest struct {
	HadesVersion string    `toml:"hades_version"`
	CreatedAt    time.Time `toml:"created_at"`
	User         string
	UID          int
	Project      string
	Name         string
	Label        string
	Host         string
	Port         int
	Worktree     string
	Root         string
	Preset       string
	DBSuffix     string   `toml:"db_suffix"`
	DBBases      []string `toml:"db_bases"` // database names minus DBSuffix
	Vhost        string
	ADE          string
}

func (r *Registry) manifestPath(label string) string {
	return filepath.Join(r.ManifestsDir, label+".toml")
}

// WriteManifest atomically writes m to ManifestsDir/<label>.toml (mode 0644).
func (r *Registry) WriteManifest(m *Manifest) error {
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
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.manifestPath(m.Label))
}

func readManifest(path string) (*Manifest, error) {
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return nil, err
		}
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return &m, nil
}

// ByLabel loads the manifest for label; fs.ErrNotExist when absent.
func (r *Registry) ByLabel(label string) (*Manifest, error) {
	return readManifest(r.manifestPath(label))
}

// All returns every manifest sorted by label. An unparsable manifest is an error.
func (r *Registry) All() ([]*Manifest, error) {
	entries, err := os.ReadDir(r.ManifestsDir)
	if err != nil {
		return nil, err
	}
	var out []*Manifest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		m, err := readManifest(filepath.Join(r.ManifestsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// canonical resolves symlinks when the path exists, else just cleans it.
func canonical(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// ByWorktree finds the manifest whose worktree is path (symlinks resolved).
func (r *Registry) ByWorktree(path string) (*Manifest, error) {
	all, err := r.All()
	if err != nil {
		return nil, err
	}
	want := canonical(path)
	for _, m := range all {
		if canonical(m.Worktree) == want {
			return m, nil
		}
	}
	return nil, fmt.Errorf("%w for worktree %s", ErrNoManifest, path)
}

// RemoveManifest deletes the manifest for label.
func (r *Registry) RemoveManifest(label string) error {
	return os.Remove(r.manifestPath(label))
}

// ManifestOwner returns the uid owning the manifest file. Ownership decisions
// use this, not the UID recorded inside the manifest.
func (r *Registry) ManifestOwner(label string) (int, error) {
	fi, err := os.Stat(r.manifestPath(label))
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("manifest %s: no owner information on this platform", label)
	}
	return int(st.Uid), nil
}
