package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	got, err := r.ByWorktree(wt)
	if err != nil || got.Label != "jarl-x" || got.DBBases[0] != "gym_nation_dev" || !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := r.ByWorktree(t.TempDir()); !errors.Is(err, ErrNoManifest) {
		t.Fatal(err)
	}
	all, _ := r.All()
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
	all, err := r.All()
	if err != nil || len(all) != 3 || all[0].Label != "a-1" || all[1].Label != "b-1" || all[2].Label != "c-1" {
		t.Fatalf("%v %v", all, err)
	}
}

func TestAllFailsOnCorruptManifest(t *testing.T) {
	r := newReg(t)
	if err := os.WriteFile(r.ManifestsDir+"/bad.toml", []byte("not = [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.All(); err == nil {
		t.Fatal("want parse error")
	}
}
