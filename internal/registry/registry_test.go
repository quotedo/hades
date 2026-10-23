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
	m := &Manifest{HadesVersion: "test", CreatedAt: time.Now().UTC().Truncate(time.Second), User: "roman", UID: 501, Project: "jarl", Name: "x", Label: "jarl-x", Host: "jarl-x.example.test", Port: 4100, Worktree: wt, Root: "/r", Preset: "phoenix", DBSuffix: "_roman_x", DBBases: []string{"gym_nation_dev"}, ADE: "paseo"}
	if err := r.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	got, err := r.ByWorktree(wt, os.Getuid())
	if err != nil || got.Label != "jarl-x" || got.DBBases[0] != "gym_nation_dev" || !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := r.ByWorktree(t.TempDir(), os.Getuid()); !errors.Is(err, ErrNoManifest) {
		t.Fatal(err)
	}
	all, _, _ := r.All()
	if len(all) != 1 {
		t.Fatal(len(all))
	}
	if err := r.RemoveManifest("jarl-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ByLabel("jarl-x"); !errors.Is(err, fs.ErrNotExist) {
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

func TestManifestOwnerIsCurrentUID(t *testing.T) {
	r := newReg(t)
	if err := r.WriteManifest(&Manifest{Label: "a-b", UID: 99999}); err != nil {
		t.Fatal(err)
	}
	uid, err := r.ManifestOwner("a-b")
	if err != nil || uid != os.Getuid() {
		t.Fatalf("uid %d err %v, want %d", uid, err, os.Getuid())
	}
	if _, err := r.ManifestOwner("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
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
		if _, err := r.ManifestOwner(label); err == nil {
			t.Errorf("ManifestOwner(%q) accepted", label)
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

func TestManifestOwnerRefusesSymlink(t *testing.T) {
	r := newReg(t)
	target := filepath.Join(t.TempDir(), "t.toml")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(r.ManifestsDir, "l.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ManifestOwner("l"); err == nil {
		t.Fatal("ManifestOwner followed a symlink")
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
