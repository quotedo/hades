// Package caddy writes and removes the one-line site files that Caddy imports
// for each worktree, and reloads Caddy.
package caddy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/naming"
)

// ReloadArgv reloads the machine's Caddy, which imports the site files.
var ReloadArgv = []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile"}

var hostName = regexp.MustCompile(`^[a-z0-9.-]+$`)

// SitePath returns the site file for a worktree label, refusing a label that
// is not a plain file name.
func SitePath(sitesDir, label string) (string, error) {
	if err := naming.CheckPlain(label); err != nil {
		return "", err
	}
	return filepath.Join(sitesDir, label+".caddy"), nil
}

// WriteSite atomically writes the site file for label via a temp file and
// rename.
func WriteSite(sitesDir, label, host string, port int) error {
	path, err := SitePath(sitesDir, label)
	if err != nil {
		return err
	}
	if !hostName.MatchString(host) {
		return fmt.Errorf("refusing host %q: must match %s", host, hostName)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("refusing port %d: must be 1..65535", port)
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

// Reload runs ReloadArgv.
func Reload(ctx context.Context, run execx.Runner) error {
	_, err := run.Run(ctx, execx.Cmd{Argv: ReloadArgv})
	return err
}
