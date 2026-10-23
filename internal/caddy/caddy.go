// Package caddy writes, validates and removes the one-line site files that
// Caddy imports for each worktree, and reloads Caddy. Reload runs as root
// through sudo, so it reads only the host config and the sites directory.
package caddy

import (
	"context"
	"errors"
	"fmt"
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

// WriteSite atomically writes the site file for label.
func WriteSite(sitesDir, label, host string, port int) error {
	if !hostName.MatchString(host) {
		return fmt.Errorf("refusing host %q: must match %s", host, hostName)
	}
	tmp, err := os.CreateTemp(sitesDir, "."+label+".*.tmp")
	if err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	_, werr := fmt.Fprintf(tmp, "import worktree %s %d\n", host, port)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write site file: %w", err)
	}
	if err := os.Rename(tmp.Name(), SitePath(sitesDir, label)); err != nil {
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

// ValidateSites checks that every *.caddy file in sitesDir is a single valid
// site line. Anything other than a regular file (such as a symlink) fails.
func ValidateSites(sitesDir string) error {
	entries, err := os.ReadDir(sitesDir) // sorted by name
	if err != nil {
		return fmt.Errorf("sites directory: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".caddy") {
			continue
		}
		path := filepath.Join(sitesDir, e.Name())
		var content []byte
		if e.Type().IsRegular() {
			if content, err = os.ReadFile(path); err != nil {
				return fmt.Errorf("read site file: %w", err)
			}
		}
		if !SiteLine.Match(content) {
			return fmt.Errorf("site file %s does not match \"import worktree <host> <port>\"", path)
		}
	}
	return nil
}

// Reload validates the site files and the Caddy config, then, unless check is
// set, reloads Caddy. Any site file failure stops before a command runs.
func Reload(ctx context.Context, cfg config.Caddy, sitesDir string, check bool, run execx.Runner) error {
	if err := ValidateSites(sitesDir); err != nil {
		return err
	}
	if _, err := run.Run(ctx, execx.Cmd{Argv: []string{"caddy", "validate", "--config", cfg.Config}}); err != nil {
		return fmt.Errorf("caddy validate: %w", err)
	}
	if check {
		return nil
	}
	if len(cfg.Reload) == 0 {
		return errors.New("caddy reload: no reload command configured")
	}
	if _, err := run.Run(ctx, execx.Cmd{Argv: cfg.Reload}); err != nil {
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
