package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/testutil"
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

// gc --owner selects by the file's owner, never by the user field.
func TestGCOwnerByFileOwnerThroughDispatcher(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)
	me := currentUserName(t)
	// The file is the current user's. (The user field must name the current
	// user too, because a laptop gc considers only those; the lifecycle tests
	// cover a user field that differs from the file owner.)
	m := &registry.Manifest{User: me, Label: "demo-mine", Name: "mine", DBSuffix: naming.Suffix(me, "mine"), Worktree: t.TempDir()}
	if err := reg.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		code, _, errs := runMain(getenv, "gc", "--dry-run", "--owner", "root")
		if code != 0 || strings.Contains(errs, "demo-mine") {
			t.Fatalf("--owner root selected a file root does not own: code=%d err=%q", code, errs)
		}
	}
	code, _, errs := runMain(getenv, "gc", "--dry-run", "--owner", me)
	if code != 0 || errs != "hades: gc: would release demo-mine\n" {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	code, _, errs = runMain(getenv, "gc", "--owner", "no-such-user-hades-test")
	if code != 1 || !strings.Contains(errs, "no-such-user-hades-test") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

// A server's up refuses a registry without the shared modes, naming the
// directory and the property, before claiming anything.
func TestServerUpRefusesRegistryWithoutSharedModes(t *testing.T) {
	reg, getenv, _ := serverFixture(t, "")
	root, wt, _ := laptopUpFixture(t, "phoenix")
	code, _, errs := runMain(getenv, "--root", root, "--worktree", wt, "--name", "feature/x", "up", "--skip-setup")
	if code != 1 || !strings.Contains(errs, reg.PortsDir+": missing the sticky bit") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if entries, _ := os.ReadDir(reg.PortsDir); len(entries) != 0 {
		t.Fatal("claimed a port")
	}
}

// caddy-reload publishes only <label>.<domain> for a label whose manifest
// the site file's owner holds.
func TestCaddyReloadAuthorisesSitesByLabelAndManifest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root caddy-reload reads only /etc/hades/host.toml")
	}
	reg, getenv, state := serverFixture(t, "")
	for _, tool := range []string{"caddy", "systemctl"} {
		testutil.StubTool(t, filepath.Join(state, "bin"), tool, "#!/bin/bash\nexit 0\n")
	}
	if err := reg.WriteManifest(&registry.Manifest{Label: "demo-a"}); err != nil {
		t.Fatal(err)
	}
	site := "import worktree demo-a.example.test 4100\n"
	if err := os.WriteFile(filepath.Join(reg.SitesDir, "demo-a.caddy"), []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(state, "live", "demo-a.caddy")
	if code, _, errs := runMain(getenv, "caddy-reload"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if got, _ := os.ReadFile(live); string(got) != site {
		t.Fatalf("live = %q", got)
	}

	// A site for a hostname outside its label, or without a manifest, is
	// refused, and the live directory keeps what it had.
	for name, content := range map[string]string{
		"demo-b.caddy": "import worktree admin.example.test 4101\n",
		"demo-c.caddy": "import worktree demo-c.example.test 4102\n",
	} {
		path := filepath.Join(reg.SitesDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if name == "demo-b.caddy" {
			if err := reg.WriteManifest(&registry.Manifest{Label: "demo-b"}); err != nil {
				t.Fatal(err)
			}
		}
		code, _, errs := runMain(getenv, "caddy-reload")
		if code != 1 || !strings.Contains(errs, "caddy-reload: site file "+path) {
			t.Fatalf("%s: code=%d err=%q", name, code, errs)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(state, "live")); len(entries) != 1 {
		t.Fatalf("live dir changed: %v", entries)
	}
}
