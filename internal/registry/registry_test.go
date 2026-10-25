package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quotedo/hades/internal/config"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	base := t.TempDir()
	r := &Registry{PortsDir: base + "/ports", SitesDir: base + "/sites", ManifestsDir: base + "/manifests", Range: config.PortRange{Lo: 4100, Hi: 4119}}
	if err := r.EnsureDirs(0o700); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestClaimPortLowestFreeAndRace(t *testing.T) {
	r := newReg(t)
	var wg sync.WaitGroup
	got := make([]int, 20)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := r.ClaimPort(fmt.Sprintf("/wt/%d", i))
			if err != nil {
				t.Error(err)
			}
			got[i] = p
		}(i)
	}
	wg.Wait()
	sort.Ints(got)
	for i, p := range got {
		if p != 4100+i {
			t.Fatalf("ports %v", got)
		}
	}
	if _, err := r.ClaimPort("/wt/extra"); !errors.Is(err, ErrNoFreePort) {
		t.Fatal(err)
	}
}

func TestReleasePortOnlyByOwner(t *testing.T) {
	r := newReg(t)
	p, _ := r.ClaimPort("/wt/a")
	if err := r.ReleasePort(p, "/wt/b"); !errors.Is(err, ErrNotOwner) {
		t.Fatal(err)
	}
	if err := r.ReleasePort(p, "/wt/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PortOwner(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestManifestRoundTripAndLookupByWorktree(t *testing.T) {
	r := newReg(t)
	wt := t.TempDir()
	m := &Manifest{HadesVersion: "test", CreatedAt: time.Now().UTC().Truncate(time.Second), User: "alice", UID: 501, Project: "shop", Name: "x", Label: "shop-x", Host: "shop-x.example.test", Port: 4100, Worktree: wt, Root: "/r", Preset: "phoenix", DBSuffix: "_alice_x", DBBases: []string{"shop_dev"}, ADE: "paseo"}
	if err := r.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	got, err := r.ByWorktree(wt, os.Getuid())
	if err != nil || got.Label != "shop-x" || got.DBBases[0] != "shop_dev" || !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := r.ByWorktree(t.TempDir(), os.Getuid()); !errors.Is(err, ErrNoManifest) {
		t.Fatal(err)
	}
	all, _, _ := r.All()
	if len(all) != 1 {
		t.Fatal(len(all))
	}
	if err := r.RemoveManifest("shop-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ByLabel("shop-x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestServerModeNeverCreatesDirs(t *testing.T) {
	r := &Registry{PortsDir: t.TempDir() + "/nope", ManifestsDir: t.TempDir(), SitesDir: t.TempDir()}
	if err := r.Check(); !errors.Is(err, ErrRegistryMissing) || !strings.Contains(err.Error(), "/nope") {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.PortsDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Check created the directory: %v", err)
	}
}

func TestAllSortedByLabel(t *testing.T) {
	r := newReg(t)
	for _, l := range []string{"c-1", "a-1", "b-1"} {
		if err := r.WriteManifest(&Manifest{Label: l}); err != nil {
			t.Fatal(err)
		}
	}
	all, _, err := r.All()
	if err != nil || len(all) != 3 || all[0].Label != "a-1" || all[1].Label != "b-1" || all[2].Label != "c-1" {
		t.Fatalf("%v %v", all, err)
	}
}

func TestAllSkipsAndReportsBadEntries(t *testing.T) {
	r := newReg(t)
	if err := r.WriteManifest(&Manifest{Label: "good-1", Worktree: "/w/good"}); err != nil {
		t.Fatal(err)
	}
	dir := r.ManifestsDir
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bad.toml", "not = [valid")
	write("evil.toml", "label = \"good-2\"\n")               // label is not its file name
	write("..toml", "label = \".\"\n")                       // label "." is not a plain name
	write("esc.toml", "label = \"../../../../escape\"\n")    // traversal
	write("notes.txt", "ignored silently: not a manifest\n") // not *.toml
	target := filepath.Join(t.TempDir(), "target.toml")
	if err := os.WriteFile(target, []byte("label = \"link\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.toml"), 0o755); err != nil {
		t.Fatal(err)
	}

	all, problems, err := r.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Label != "good-1" || all[0].OwnerUID() != os.Getuid() || all[0].Path() != filepath.Join(dir, "good-1.toml") {
		t.Fatalf("%+v", all)
	}
	reasons := map[string]string{}
	for _, p := range problems {
		reasons[filepath.Base(p.Path)] = p.Reason
		if !strings.HasPrefix(p.String(), "ignored manifest "+p.Path+": ") {
			t.Fatal(p.String())
		}
	}
	want := map[string]string{
		"bad.toml":  "",
		"evil.toml": "not its file name",
		"..toml":    "plain file name",
		"esc.toml":  "plain file name",
		"link.toml": "not a regular file",
		"sub.toml":  "not a regular file",
	}
	if len(reasons) != len(want) {
		t.Fatalf("%v", reasons)
	}
	for name, sub := range want {
		got, ok := reasons[name]
		if !ok || !strings.Contains(got, sub) || got == "" {
			t.Errorf("%s: %q (want %q)", name, got, sub)
		}
	}

	// The broken entries do not hide the good one from lookups.
	if m, err := r.ByWorktree("/w/good", os.Getuid()); err != nil || m.Label != "good-1" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := r.ByLabel("link"); err == nil {
		t.Fatal("ByLabel followed a symlink")
	}
	if _, err := r.ByLabel("evil"); err == nil {
		t.Fatal("ByLabel accepted a manifest recording another label")
	}
}

func TestAllListingFailureIsAnError(t *testing.T) {
	r := newReg(t)
	r.ManifestsDir = filepath.Join(t.TempDir(), "missing")
	if _, _, err := r.All(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestLabelGuardEverywhere(t *testing.T) {
	r := newReg(t)
	outside := filepath.Join(filepath.Dir(r.ManifestsDir), "victim.toml")
	if err := os.WriteFile(outside, []byte("label = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"", ".", "..", "../victim", "a/b", "/etc/passwd"} {
		if _, err := r.ManifestPath(label); err == nil {
			t.Errorf("ManifestPath(%q) accepted", label)
		}
		if _, err := r.ByLabel(label); err == nil {
			t.Errorf("ByLabel(%q) accepted", label)
		}
		if err := r.RemoveManifest(label); err == nil {
			t.Errorf("RemoveManifest(%q) accepted", label)
		}
		if err := r.WriteManifest(&Manifest{Label: label}); err == nil {
			t.Errorf("WriteManifest(%q) accepted", label)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("a guarded call removed a file outside the registry")
	}
}

func TestByWorktreeOnlySeesTheCallersManifests(t *testing.T) {
	r := newReg(t)
	wt := t.TempDir()
	if err := r.WriteManifest(&Manifest{Label: "demo-x", Worktree: wt}); err != nil {
		t.Fatal(err)
	}
	// Another uid naming the same path does not see this user's manifest, and
	// so a manifest planted by another user is invisible to this one.
	if _, err := r.ByWorktree(wt, os.Getuid()+1); !errors.Is(err, ErrNoManifest) {
		t.Fatal(err)
	}
	if m, err := r.ByWorktree(wt, os.Getuid()); err != nil || m.Label != "demo-x" {
		t.Fatalf("%+v %v", m, err)
	}
	// Root sees every owner.
	if m, err := r.ByWorktree(wt, 0); err != nil || m.Label != "demo-x" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestManifestTOMLKeysAreSnakeCase(t *testing.T) {
	r := newReg(t)
	m := &Manifest{HadesVersion: "v", User: "u", UID: 1, Project: "p", Name: "n", Label: "p-n", Host: "h", Port: 4100, Worktree: "/w", Root: "/r", Preset: "go", DBSuffix: "_u_n", DBBases: []string{"b"}, Vhost: "p-n", ADE: "shell"}
	if err := r.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.ManifestsDir, "p-n.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		if k, _, ok := strings.Cut(line, " = "); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	want := []string{"ade", "created_at", "db_bases", "db_suffix", "hades_version", "host", "label", "name", "port", "preset", "project", "root", "uid", "user", "vhost", "worktree"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys %v\n%s", keys, data)
	}
}

// chmodRegistry sets mode on every registry directory, skipping the test
// where the filesystem does not keep the setgid bit (failing under CI).
func chmodRegistry(t *testing.T, r *Registry, mode os.FileMode) {
	t.Helper()
	skip := t.Skipf
	if os.Getenv("CI") != "" {
		skip = t.Fatalf
	}
	for _, d := range r.dirs() {
		if err := os.Chmod(d, mode); err != nil {
			skip("cannot chmod %s here: %v", d, err)
		}
		if fi, err := os.Stat(d); err != nil || fi.Mode()&(os.ModeSetgid|os.ModeSticky) != mode&(os.ModeSetgid|os.ModeSticky) {
			skip("filesystem does not keep the setgid and sticky bits")
		}
	}
}

// ownSharedDirs makes this test's own uid the one shared registry
// directories must be owned by, since a test cannot create root-owned ones.
func ownSharedDirs(t *testing.T) {
	t.Helper()
	prev := SharedDirOwner
	SharedDirOwner = os.Geteuid()
	t.Cleanup(func() { SharedDirOwner = prev })
}

func TestSharedCheckRequiresModeAndGroup(t *testing.T) {
	r := newReg(t)
	r.Shared = true
	chmodRegistry(t, r, 0o775|os.ModeSetgid|os.ModeSticky)
	// A developer-owned 3775 directory is refused: its owner could remove
	// anyone's entries.
	if os.Geteuid() != 0 {
		if err := r.Check(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%s: owned by uid %d, not root", r.PortsDir, os.Geteuid())) {
			t.Fatalf("developer-owned: %v", err)
		}
	}
	ownSharedDirs(t)
	if err := r.Check(); err != nil {
		if strings.Contains(err.Error(), "not a member") && os.Getenv("CI") == "" {
			t.Skipf("test user is not in the temp dir's group: %v", err)
		}
		t.Fatalf("3775: %v", err)
	}
	for _, c := range []struct {
		mode os.FileMode
		want string
	}{
		{0o775 | os.ModeSetgid, "missing the sticky bit"},             // 2775
		{0o777 | os.ModeSetgid | os.ModeSticky, "world-writable"},     // 3777
		{0o775 | os.ModeSticky, "missing the setgid bit"},             // 1775
		{0o755 | os.ModeSetgid | os.ModeSticky, "not group-writable"}, // 3755
	} {
		chmodRegistry(t, r, c.mode)
		err := r.Check()
		if err == nil || !strings.Contains(err.Error(), "wrong mode or group") || !strings.Contains(err.Error(), r.PortsDir+": "+c.want) {
			t.Errorf("%o: %v", c.mode, err)
		}
	}
	// A registry that is not shared (laptop mode) is checked for existence only.
	r.Shared = false
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
}

// Root rewriting another user's manifest (gc keeping a manifest after a
// failed step) keeps the file's owner. Only root can chown, so this runs as
// root only; CI runs it under sudo on Linux.
func TestRootRewriteKeepsManifestOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown the manifest to another uid; CI runs it under sudo on Linux")
	}
	const nobody = 65534
	r := newReg(t)
	if err := r.WriteManifest(&Manifest{Label: "demo-x", Port: 4100}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Join(r.ManifestsDir, "demo-x.toml"), nobody, -1); err != nil {
		t.Fatal(err)
	}
	m, err := r.ByLabel("demo-x")
	if err != nil || m.OwnerUID() != nobody {
		t.Fatalf("%+v %v", m, err)
	}
	m.Port = 0
	if err := r.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	if m, err := r.ByLabel("demo-x"); err != nil || m.OwnerUID() != nobody {
		t.Fatalf("%+v %v", m, err)
	}
}
