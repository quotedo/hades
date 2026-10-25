package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/registry"
)

// plantBroken puts a garbage manifest and a symlinked one into reg.
func plantBroken(t *testing.T, reg *registry.Registry) (garbage, link string) {
	t.Helper()
	garbage = filepath.Join(reg.ManifestsDir, "garbage.toml")
	if err := os.WriteFile(garbage, []byte("not = [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(reg.ManifestsDir, "link.toml")
	if err := os.Symlink("/etc/hosts", link); err != nil {
		t.Fatal(err)
	}
	return garbage, link
}

// Broken manifests are reported by ls and doctor and never stop ls, port,
// up or down.
func TestBrokenManifestsAreReportedNotFatal(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)
	garbage, link := plantBroken(t, reg)
	me := currentUserName(t)
	if err := reg.WriteManifest(&registry.Manifest{User: me, Label: "demo-feature-x", Name: "feature/x", Port: 4100, Worktree: wt, Root: root}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wt, filepath.Join(reg.PortsDir, "4100")); err != nil {
		t.Fatal(err)
	}

	code, out, errs := runMain(getenv, "ls")
	if code != 0 || !strings.Contains(out, "demo-feature-x") ||
		!strings.Contains(errs, "hades: ls: ignored manifest "+garbage+": ") ||
		!strings.Contains(errs, "hades: ls: ignored manifest "+link+": not a regular file") {
		t.Fatalf("ls: code=%d out=%q err=%q", code, out, errs)
	}

	flags := []string{"--root", root, "--worktree", wt, "--name", "feature/x"}
	code, out, errs = runMain(getenv, append(flags, "port")...)
	if code != 0 || out != "4100\n" {
		t.Fatalf("port: code=%d out=%q err=%q", code, out, errs)
	}

	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["manifests"]; c.OK || c.Warn || !strings.Contains(c.Detail, "ignored manifest "+garbage) || !strings.Contains(c.Detail, "ignored manifest "+link) {
		t.Fatalf("manifests = %+v", c)
	}

	code, _, errs = runMain(getenv, append(flags, "up", "--dry-run")...)
	if code != 0 {
		t.Fatalf("up: code=%d err=%q", code, errs)
	}

	var o, e bytes.Buffer
	if code := Main(append(flags, "down", "--strict"), &o, &e, getenv); code != 0 || e.Len() != 0 {
		t.Fatalf("down: code=%d err=%q", code, e.String())
	}
	if _, err := reg.ByLabel("demo-feature-x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("down left the manifest")
	}
	if _, err := os.Lstat(garbage); err != nil {
		t.Fatal("down touched the broken manifest")
	}
}

func TestDoctorManifestsPassWhenClean(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)
	if err := reg.WriteManifest(&registry.Manifest{Label: "demo-a"}); err != nil {
		t.Fatal(err)
	}
	_, checks, _ := doctorChecks(t, getenv)
	if c := checks["manifests"]; !c.OK || c.Detail != "1 manifests readable" {
		t.Fatalf("manifests = %+v", c)
	}
}
