// Package caddy writes, validates and removes the one-line site files that
// Caddy imports for each worktree, and reloads Caddy. Reload runs as root
// through sudo, so it reads only the host config, the sites directory and the
// type and owner (never the contents) of the matching manifests.
package caddy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/naming"
)

// SiteLine is the only content a site file may have.
var SiteLine = regexp.MustCompile(`^import worktree [a-z0-9.-]+ [0-9]+\n$`)

var hostName = regexp.MustCompile(`^[a-z0-9.-]+$`)

// DefaultBinary is where hades is installed on a server; sudoers allows
// exactly `<DefaultBinary> caddy-reload`.
const DefaultBinary = "/usr/local/bin/hades"

// SitePath returns the site file for a worktree label, refusing a label that
// is not a plain file name.
func SitePath(sitesDir, label string) (string, error) {
	if err := naming.CheckPlain(label); err != nil {
		return "", err
	}
	return filepath.Join(sitesDir, label+".caddy"), nil
}

// maxSiteFile bounds what is read from a site file; a valid one is far shorter.
const maxSiteFile = 256

// cmdEnv is the fixed environment of every command Reload runs as root.
var cmdEnv = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}

// WriteSite atomically writes the site file for label.
func WriteSite(sitesDir, label, host string, port int) error {
	if err := naming.CheckPlain(label); err != nil {
		return err
	}
	if !hostName.MatchString(host) {
		return fmt.Errorf("refusing host %q: must match %s", host, hostName)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("refusing port %d: must be 1..65535", port)
	}
	return writeAtomic(sitesDir, label, []byte(fmt.Sprintf("import worktree %s %d\n", host, port)))
}

// writeAtomic writes content to <dir>/<label>.caddy via a temp file and rename.
func writeAtomic(dir, label string, content []byte) error {
	path, err := SitePath(dir, label)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+label+".*.tmp")
	if err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	_, werr := tmp.Write(content)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	return nil
}

// RemoveSite deletes the site file for label; an absent file is success. A
// label that is not a plain file name is an error.
func RemoveSite(sitesDir, label string) error {
	path, err := SitePath(sitesDir, label)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove site file: %w", err)
	}
	return nil
}

// readSites reads every *.caddy file in h.SitesDir exactly once, in name
// order, and returns the validated contents keyed by label. A site file is
// valid when it is a regular file (not a symlink) of at most maxSiteFile
// bytes matching SiteLine, its host is exactly <label>.<domain>, and
// manifests/<label>.toml is a regular file owned by the same uid as the site
// file, so a developer can publish only the hostnames of worktrees they hold.
func readSites(h *config.Host) (map[string][]byte, error) {
	if h.Domain == "" {
		return nil, errors.New("site files: no domain configured")
	}
	entries, err := os.ReadDir(h.SitesDir) // sorted by name
	if err != nil {
		return nil, fmt.Errorf("sites directory: %w", err)
	}
	sites := map[string][]byte{}
	for _, e := range entries {
		label, ok := strings.CutSuffix(e.Name(), ".caddy")
		if !ok {
			continue
		}
		path := filepath.Join(h.SitesDir, e.Name())
		if err := naming.CheckPlain(label); err != nil {
			return nil, fmt.Errorf("site file %s: %w", path, err)
		}
		var content []byte
		uid := -1
		if e.Type().IsRegular() {
			if content, uid, err = readSite(path); err != nil {
				return nil, err
			}
		}
		if len(content) > maxSiteFile || !SiteLine.Match(content) {
			return nil, fmt.Errorf("site file %s does not match \"import worktree <host> <port>\"", path)
		}
		if host, want := strings.Fields(string(content))[2], label+"."+h.Domain; host != want {
			return nil, fmt.Errorf("site file %s: host %s is not %s", path, host, want)
		}
		if err := checkSiteManifest(h.ManifestsDir, label, path, uid); err != nil {
			return nil, err
		}
		sites[label] = content
	}
	return sites, nil
}

// readSite reads at most maxSiteFile+1 bytes of the site file at path and
// returns them with the uid owning the file that was read.
func readSite(path string) ([]byte, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read site file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("read site file: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || !ok {
		return nil, 0, fmt.Errorf("site file %s is not a regular file", path)
	}
	content, err := io.ReadAll(io.LimitReader(f, maxSiteFile+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read site file: %w", err)
	}
	return content, int(st.Uid), nil
}

// checkSiteManifest requires manifests/<label>.toml to be a regular file
// owned by uid, the owner of the site file at sitePath.
func checkSiteManifest(manifestsDir, label, sitePath string, uid int) error {
	mpath := filepath.Join(manifestsDir, label+".toml")
	fi, err := os.Lstat(mpath)
	if err != nil {
		return fmt.Errorf("site file %s: manifest %s: %w", sitePath, mpath, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || !ok {
		return fmt.Errorf("site file %s: manifest %s is not a regular file", sitePath, mpath)
	}
	if int(st.Uid) != uid {
		return fmt.Errorf("site file %s is owned by uid %d, but manifest %s by uid %d", sitePath, uid, mpath, st.Uid)
	}
	return nil
}

// mirror makes liveDir hold exactly the given site files.
func mirror(liveDir string, sites map[string][]byte) error {
	for label, content := range sites {
		if err := writeAtomic(liveDir, label, content); err != nil {
			return fmt.Errorf("mirror to %s: %w", liveDir, err)
		}
	}
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return fmt.Errorf("mirror to %s: %w", liveDir, err)
	}
	for _, e := range entries {
		label, ok := strings.CutSuffix(e.Name(), ".caddy")
		if !ok {
			continue
		}
		if _, keep := sites[label]; !keep {
			if err := os.Remove(filepath.Join(liveDir, e.Name())); err != nil {
				return fmt.Errorf("mirror to %s: %w", liveDir, err)
			}
		}
	}
	return nil
}

// Reload validates the site files of h.SitesDir against h.Domain and
// h.ManifestsDir, mirrors the validated bytes into h.Caddy.LiveDir (the only
// directory Caddy imports), validates the Caddy config and reloads Caddy.
// With check it validates only and touches nothing. A site file failure stops
// before anything else happens.
func Reload(ctx context.Context, h *config.Host, check bool, run execx.Runner) error {
	sites, err := readSites(h)
	if err != nil {
		return err
	}
	cfg := h.Caddy
	if !check {
		if cfg.LiveDir == "" {
			return errors.New("caddy reload: no live_dir configured")
		}
		if err := mirror(cfg.LiveDir, sites); err != nil {
			return err
		}
	}
	if _, err := run.Run(ctx, execx.Cmd{Argv: []string{"caddy", "validate", "--config", cfg.Config}, Env: cmdEnv}); err != nil {
		return fmt.Errorf("caddy validate: %w", err)
	}
	if check {
		return nil
	}
	if len(cfg.Reload) == 0 {
		return errors.New("caddy reload: no reload command configured")
	}
	if _, err := run.Run(ctx, execx.Cmd{Argv: cfg.Reload, Env: cmdEnv}); err != nil {
		return fmt.Errorf("caddy reload: %w", err)
	}
	return nil
}

// BinaryPath returns HADES_BIN when set (tests), else DefaultBinary.
func BinaryPath(getenv func(string) string) string {
	if b := getenv("HADES_BIN"); b != "" {
		return b
	}
	return DefaultBinary
}

// SudoReload runs `sudo -n <bin> caddy-reload`; with check it passes --check,
// which validates without mirroring or reloading anything.
func SudoReload(ctx context.Context, run execx.Runner, bin string, check bool) error {
	argv, what := []string{"sudo", "-n", bin, "caddy-reload"}, "sudo caddy-reload"
	if check {
		argv, what = append(argv, "--check"), what+" --check"
	}
	if _, err := run.Run(ctx, execx.Cmd{Argv: argv}); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}
