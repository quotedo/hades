package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/registry"
)

// laptopRegistry creates the laptop registry under getenv's HOME.
func laptopRegistry(t *testing.T, getenv func(string) string) *registry.Registry {
	t.Helper()
	host, err := config.LaptopHost(getenv("HOME"), getenv)
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New(host)
	if err := reg.EnsureDirs(0o700); err != nil {
		t.Fatal(err)
	}
	return reg
}

func currentUserName(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return u.Username
}

func TestDownWithoutManifestExitsZero(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "phoenix")
	var out, errb bytes.Buffer
	code := Main([]string{"--root", root, "--worktree", wt, "--name", "feature/x", "down", "--strict"}, &out, &errb, getenv)
	if code != 0 || !strings.Contains(errb.String(), "down: skipped manifest: no manifest for worktree") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestDownRemovesManifestThroughDispatcher(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)
	if err := reg.WriteManifest(&registry.Manifest{User: currentUserName(t), UID: os.Getuid(), Label: "demo-feature-x", Worktree: wt, Root: root}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Main([]string{"--root", root, "--worktree", wt, "--name", "feature/x", "down", "--strict"}, &out, &errb, getenv)
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if _, err := reg.ByLabel("demo-feature-x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("manifest left")
	}
}

func TestDownAndGCRejectUnknownFlagsAndArgs(t *testing.T) {
	for _, args := range [][]string{{"down", "--bogus"}, {"down", "extra"}, {"gc", "--bogus"}, {"gc", "extra"}} {
		var out, errb bytes.Buffer
		if code := Main(args, &out, &errb, func(string) string { return "" }); code != 2 || !strings.Contains(errb.String(), "usage: hades "+args[0]) {
			t.Fatalf("%v: code=%d err=%q", args, code, errb.String())
		}
	}
}

func TestGCRefusesNonRootOnServer(t *testing.T) {
	state := t.TempDir()
	hostFile := filepath.Join(state, "host.toml")
	content := `domain = "example.test"
port_range = "4100-4119"
ports_dir = "` + filepath.Join(state, "ports") + `"
sites_dir = "` + filepath.Join(state, "sites") + `"
manifests_dir = "` + filepath.Join(state, "manifests") + `"
[caddy]
config = "/etc/caddy/Caddyfile"
live_dir = "/etc/caddy/worktrees"
reload = ["caddy", "reload"]
[postgres]
host = "127.0.0.1"
port = 5432
user = "postgres"
password = "postgres"
`
	if err := os.WriteFile(hostFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := geteuid
	geteuid = func() int { return 1000 }
	t.Cleanup(func() { geteuid = prev })
	getenv := func(k string) string {
		if k == "HADES_HOST_CONFIG" {
			return hostFile
		}
		return ""
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"gc"}, &out, &errb, getenv); code != 1 || errb.String() != "hades: gc: must run as root on a server\n" {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	// A host config the user cannot read still gets the root message, not a
	// permission error.
	if err := os.Chmod(hostFile, 0); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := Main([]string{"gc"}, &out, &errb, getenv); code != 1 || errb.String() != "hades: gc: must run as root on a server\n" {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestGCOnLaptopReleasesOnlyTheCurrentUsersVanishedWorktrees(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	reg := laptopRegistry(t, getenv)
	gone := filepath.Join(t.TempDir(), "gone")
	for _, m := range []*registry.Manifest{
		{User: currentUserName(t), Label: "demo-gone", Worktree: gone},
		{User: "someone-else", Label: "demo-theirs", Worktree: gone},
	} {
		if err := reg.WriteManifest(m); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	if code := Main([]string{"gc", "--dry-run"}, &out, &errb, getenv); code != 0 || errb.String() != "hades: gc: would release demo-gone\n" {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if _, err := reg.ByLabel("demo-gone"); err != nil {
		t.Fatal("dry run removed the manifest")
	}

	errb.Reset()
	if code := Main([]string{"gc"}, &out, &errb, getenv); code != 0 || !strings.Contains(errb.String(), "hades: gc: released demo-gone\n") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if _, err := reg.ByLabel("demo-gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("manifest left")
	}
	if _, err := reg.ByLabel("demo-theirs"); err != nil {
		t.Fatal("released another user's manifest")
	}
}

func TestGCOnLaptopWithoutRegistryIsANoOp(t *testing.T) {
	_, _, getenv := laptopUpFixture(t, "phoenix")
	var out, errb bytes.Buffer
	if code := Main([]string{"gc"}, &out, &errb, getenv); code != 0 || errb.Len() != 0 {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}
