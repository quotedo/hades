// Package caddy writes, validates and removes the one-line site files that
// Caddy imports for each worktree, and reloads Caddy. Reload runs as root
// through sudo, so it reads only the host config and the sites directory.
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

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
)

// SiteLine is the only content a site file may have.
var SiteLine = regexp.MustCompile(`^import worktree [a-z0-9.-]+ [0-9]+\n$`)

var hostName = regexp.MustCompile(`^[a-z0-9.-]+$`)

// DefaultBinary is where hades is installed on a server; sudoers allows
// exactly `<DefaultBinary> caddy-reload`.
const DefaultBinary = "/usr/local/bin/hades"

// SitePath returns the site file for a worktree label.
func SitePath(sitesDir, label string) string {
	return filepath.Join(sitesDir, label+".caddy")
}

// maxSiteFile bounds what is read from a site file; a valid one is far shorter.
const maxSiteFile = 256

// cmdEnv is the fixed environment of every command Reload runs as root.
var cmdEnv = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}

// WriteSite atomically writes the site file for label.
func WriteSite(sitesDir, label, host string, port int) error {
	if label == "" || label != filepath.Base(label) || label == "." || label == ".." {
		return fmt.Errorf("refusing label %q: must be a plain file name", label)
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
	if err := os.Rename(tmp.Name(), SitePath(dir, label)); err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	return nil
}

// RemoveSite deletes the site file for label; an absent file is success.
func RemoveSite(sitesDir, label string) error {
	if err := os.Remove(SitePath(sitesDir, label)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove site file: %w", err)
	}
	return nil
}

// readSites reads every *.caddy file in sitesDir exactly once, in name order,
// and returns the validated contents keyed by label. Anything other than a
// regular file (such as a symlink), or longer than maxSiteFile, is invalid.
func readSites(sitesDir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(sitesDir) // sorted by name
	if err != nil {
		return nil, fmt.Errorf("sites directory: %w", err)
	}
	sites := map[string][]byte{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".caddy") {
			continue
		}
		path := filepath.Join(sitesDir, e.Name())
		var content []byte
		if e.Type().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return nil, fmt.Errorf("read site file: %w", err)
			}
			content, err = io.ReadAll(io.LimitReader(f, maxSiteFile+1))
			f.Close()
			if err != nil {
				return nil, fmt.Errorf("read site file: %w", err)
			}
		}
		if len(content) > maxSiteFile || !SiteLine.Match(content) {
			return nil, fmt.Errorf("site file %s does not match \"import worktree <host> <port>\"", path)
		}
		sites[strings.TrimSuffix(e.Name(), ".caddy")] = content
	}
	return sites, nil
}

// ValidateSites checks that every *.caddy file in sitesDir is a single valid
// site line.
func ValidateSites(sitesDir string) error {
	_, err := readSites(sitesDir)
	return err
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

// Reload validates the site files, mirrors the validated bytes into
// cfg.LiveDir (the only directory Caddy imports), validates the Caddy config
// and reloads Caddy. With check it validates only and touches nothing. A site
// file failure stops before anything else happens.
func Reload(ctx context.Context, cfg config.Caddy, sitesDir string, check bool, run execx.Runner) error {
	sites, err := readSites(sitesDir)
	if err != nil {
		return err
	}
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

// SudoReload runs `sudo -n <bin> caddy-reload`.
func SudoReload(ctx context.Context, run execx.Runner, bin string) error {
	if _, err := run.Run(ctx, execx.Cmd{Argv: []string{"sudo", "-n", bin, "caddy-reload"}}); err != nil {
		return fmt.Errorf("sudo caddy-reload: %w", err)
	}
	return nil
}
