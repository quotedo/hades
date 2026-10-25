package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/registry"
)

// nobody is the uid a root-only test gives a second owner's files.
const nobody = 65534

// plant writes a manifest file by hand, as another developer of the host
// could, bypassing WriteManifest's checks.
func plant(t *testing.T, d Deps, file, content string) string {
	t.Helper()
	path := filepath.Join(d.Reg.ManifestsDir, file)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasError(errs []error, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), sub) {
			return true
		}
	}
	return false
}

func hasProblem(ps []registry.Problem, file, sub string) bool {
	for _, p := range ps {
		if filepath.Base(p.Path) == file && strings.Contains(p.Reason, sub) {
			return true
		}
	}
	return false
}

// A manifest naming another worktree's label, databases or vhost is refused
// by root gc and reported; the victim's resources and files stay.
func TestGCRefusesForgedClaims(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	rmq := &fakeRabbit{}
	d.Rabbit = rmq
	mustUp(t, d, c) // the victim: demo-feature-x, still alive
	victimDBs := []string{"demo_dev_alice_feature_x", "demo_dev_bob_feature_x"}
	d.PG.(*fakePG).names = victimDBs

	gone := filepath.Join(t.TempDir(), "gone")
	forgedVhost := plant(t, d, "evil1.toml", fmt.Sprintf("label = \"evil1\"\nname = \"feature/x\"\ndb_suffix = \"_alice_feature_x\"\nvhost = \"demo-feature-x\"\nworktree = %q\n", gone))
	forgedSuffix := plant(t, d, "evil2.toml", fmt.Sprintf("label = \"evil2\"\nname = \"feature/x\"\ndb_suffix = \"_bob_feature_x\"\ndb_bases = [\"demo_dev\"]\nworktree = %q\n", gone))
	plant(t, d, "evil3.toml", fmt.Sprintf("label = \"demo-feature-x\"\nname = \"feature/x\"\ndb_suffix = \"_alice_feature_x\"\ndb_bases = [\"demo_dev\"]\nworktree = %q\n", gone))

	rep, err := GC(context.Background(), d, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Released) != 0 {
		t.Fatalf("released %v", rep.Released)
	}
	if !hasError(rep.Errors, "refusing "+forgedVhost+": vhost") || !hasError(rep.Errors, "refusing "+forgedSuffix+": db_suffix") {
		t.Fatalf("%v", rep.Errors)
	}
	if !hasProblem(rep.Ignored, "evil3.toml", "not its file name") {
		t.Fatalf("%+v", rep.Ignored)
	}
	if len(d.PG.(*fakePG).dropped) != 0 || len(rmq.deleted) != 0 {
		t.Fatalf("dropped %v, deleted %v", d.PG.(*fakePG).dropped, rmq.deleted)
	}
	for _, p := range []string{forgedVhost, forgedSuffix, filepath.Join(d.Reg.ManifestsDir, "demo-feature-x.toml"), sitePath(t, d, "demo-feature-x")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != c.Worktree {
		t.Fatal("victim's port released")
	}
}

// A label that is a path never reaches the file system: gc ignores the
// manifest and the file it points at survives.
func TestGCIgnoresTraversalLabel(t *testing.T) {
	d, _ := newServerDeps(t, "phoenix")
	state := filepath.Dir(d.Reg.ManifestsDir)
	victim := filepath.Join(state, "victim.toml")
	victimSite := filepath.Join(state, "victim.caddy")
	for _, p := range []string{victim, victimSite} {
		if err := os.WriteFile(p, []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plant(t, d, "esc.toml", "label = \"../victim\"\nworktree = \"/nonexistent\"\n")
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || len(rep.Released) != 0 || !hasProblem(rep.Ignored, "esc.toml", "plain file name") {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, p := range []string{victim, victimSite} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s removed: %v", p, err)
		}
	}
	// The same label in memory is refused by every release step that would
	// build a path from it.
	m := &registry.Manifest{Label: "../victim"}
	if res, _, _ := release(context.Background(), d, m, false, nil); len(res) == 0 {
		t.Fatal("no results")
	} else {
		for _, r := range res {
			if r.Step == "site" && r.Err == nil {
				t.Fatalf("site step accepted the label: %+v", res)
			}
		}
	}
	for _, p := range []string{victim, victimSite} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s removed: %v", p, err)
		}
	}
}

// Broken entries in the manifests directory never stop anyone else's up,
// down or gc.
func TestGarbageAndSymlinkedManifestsDoNotStopUpOrDown(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	plant(t, d, "garbage.toml", "not = [valid")
	if err := os.Symlink("/etc/hosts", filepath.Join(d.Reg.ManifestsDir, "link.toml")); err != nil {
		t.Fatal(err)
	}
	mustUp(t, d, c)
	rep, err := GC(context.Background(), d, "", true)
	if err != nil || !hasProblem(rep.Ignored, "garbage.toml", "") || !hasProblem(rep.Ignored, "link.toml", "not a regular file") {
		t.Fatalf("%+v %v", rep, err)
	}
	res, err := Down(context.Background(), d, c, true)
	if err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := d.Reg.ByLabel("demo-feature-x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("manifest left")
	}
}

// Another user's manifest naming my worktree is invisible to my up and down.
func TestForeignManifestForMyWorktreeIsIgnored(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	// Seen from uid 999, the manifest alice's file holds is someone else's:
	// up derives the label, finds it taken, and down finds nothing of its own.
	d.User.UID = 999
	if _, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err == nil {
		t.Fatal("up took over another uid's label")
	} else if le := (*ErrLabelTaken)(nil); !errors.As(err, &le) {
		t.Fatal(err)
	}
	res, err := Down(context.Background(), d, c, true)
	if err != nil || len(res) != 1 || !res[0].Skipped || !strings.Contains(res[0].Reason, "no manifest found") {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := d.Reg.ByLabel("demo-feature-x"); err != nil {
		t.Fatal("another uid's down removed the manifest")
	}
}

// Down as root verifies a foreign manifest's claims like gc does.
func TestDownAsRootRefusesForgedManifest(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the manifest would be root's own")
	}
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	m, err := d.Reg.ByLabel("demo-feature-x")
	if err != nil {
		t.Fatal(err)
	}
	m.DBSuffix = "_bob_feature_x"
	if err := d.Reg.WriteManifest(m); err != nil {
		t.Fatal(err)
	}
	d.PG.(*fakePG).names = []string{"demo_dev_bob_feature_x"}
	d.User.UID = 0
	res, err := Down(context.Background(), d, c, true)
	if err == nil || len(res) != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "refusing ") {
		t.Fatalf("%+v %v", res, err)
	}
	if len(d.PG.(*fakePG).dropped) != 0 {
		t.Fatal(d.PG.(*fakePG).dropped)
	}
	if _, err := d.Reg.ByLabel("demo-feature-x"); err != nil {
		t.Fatal("manifest removed")
	}
}

// The suffix is checked against the owner's login from the user database,
// and an owner uid without a login is refused, never checked against the
// user field the file's author wrote.
func TestGCChecksSuffixAgainstOwnerLogin(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	d.LoginOf = func(int) (string, error) { return "bob", nil }
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || len(rep.Released) != 0 || !hasError(rep.Errors, "the suffix of owner bob") {
		t.Fatalf("%+v %v", rep, err)
	}

	d.LoginOf = func(uid int) (string, error) { return "", fmt.Errorf("no such uid %d", uid) }
	rep, err = GC(context.Background(), d, "", false)
	if err != nil || len(rep.Released) != 0 || !hasError(rep.Errors, fmt.Sprintf("cannot resolve owner uid %d", os.Getuid())) {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := d.Reg.ByLabel("demo-feature-x"); err != nil {
		t.Fatal("refused manifest removed")
	}
}

// The re-reviewer's planted file: user = "bob" and bob's suffix, owned by a
// uid the user database does not know. Root gc refuses it.
func TestGCRefusesUnresolvableOwner(t *testing.T) {
	d, _ := newServerDeps(t, "phoenix")
	d.PG.(*fakePG).names = []string{"demo_dev_bob_feature_x"}
	// The real user database, asked about a uid it does not know, as if the
	// file were owned by that uid (tests cannot chown).
	const unknown = 4000000000
	realLookup := (&Deps{}).loginOf
	if _, err := realLookup(unknown); err == nil {
		t.Skipf("uid %d resolves on this machine", unknown)
	}
	d.LoginOf = func(int) (string, error) { return realLookup(unknown) }
	path := plant(t, d, "evil.toml", "user = \"bob\"\nlabel = \"evil\"\nname = \"feature/x\"\ndb_suffix = \"_bob_feature_x\"\ndb_bases = [\"demo_dev\"]\nworktree = \"/gone\"\n")
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || len(rep.Released) != 0 || !hasError(rep.Errors, "refusing "+path+": cannot resolve owner uid") {
		t.Fatalf("%+v %v", rep, err)
	}
	if len(d.PG.(*fakePG).dropped) != 0 {
		t.Fatal(d.PG.(*fakePG).dropped)
	}
}

// A manifest kept after a failed step no longer claims the port it released,
// so the port's reuse by the next up does not stop gc from converging.
func TestKeptManifestPortReuseConverges(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	d.PG.(*fakePG).names = []string{"demo_dev_alice_feature_x"}
	d.PG.(*fakePG).failDrop = true
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	if rep, _ := GC(context.Background(), d, "", false); len(rep.Released) != 0 || len(rep.Errors) != 1 {
		t.Fatalf("gc1: %+v", rep)
	}
	kept, err := d.Reg.ByLabel("demo-feature-x")
	if err != nil || kept.Port != 0 || !reflect.DeepEqual(kept.DBBases, []string{"demo_dev", "demo_test"}) || kept.OwnerUID() != os.Getuid() {
		t.Fatalf("kept manifest %+v %v", kept, err)
	}
	d2, c2 := newServerDepsSharing(t, d, "phoenix", "alice")
	mustUp(t, d2, c2)
	if owner, _ := d.Reg.PortOwner(4100); owner != c2.Worktree {
		t.Fatalf("port 4100 -> %s", owner)
	}
	d.PG.(*fakePG).failDrop = false
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x"}) || len(rep.Errors) != 0 {
		t.Fatalf("gc2: %+v %v", rep, err)
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != c2.Worktree {
		t.Fatal("gc released the reused port")
	}
}

// An old manifest that still records a port someone else now holds (from a
// hades before kept manifests were rewritten) is skipped, not refused.
func TestGCStalePortOfAnotherWorktreeIsASkip(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d.Reg.PortsDir, "4100")
	other := t.TempDir()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x"}) || len(rep.Errors) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != other {
		t.Fatal("released another worktree's port")
	}
}

// After a down that kept the manifest, the owner's next up in the same
// worktree claims a fresh port instead of failing on the released one.
func TestUpAfterKeptManifest(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	d.PG.(*fakePG).names = []string{"demo_dev_alice_feature_x"}
	d.PG.(*fakePG).failDrop = true
	if _, err := Down(context.Background(), d, c, false); err != nil {
		t.Fatal(err)
	}
	if m, err := d.Reg.ByLabel("demo-feature-x"); err != nil || m.Port != 0 {
		t.Fatalf("%+v %v", m, err)
	}
	d.PG.(*fakePG).failDrop = false
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || out.Manifest.Port != 4100 {
		t.Fatalf("%+v %v", out, err)
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != c.Worktree {
		t.Fatal(owner)
	}
}

// A manifest written before kept manifests were rewritten may still record a
// port whose link is gone or is someone else's; up claims a new one.
func TestUpReclaimsPortWhenRecordedLinkIsGoneOrForeign(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	link := filepath.Join(d.Reg.PortsDir, "4100")
	other := t.TempDir()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	d.Log = &log
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || out.Manifest.Port != 4101 || !strings.Contains(log.String(), "is now claimed by "+other+"; claiming a new port") {
		t.Fatalf("%+v %v\n%s", out, err, log.String())
	}
	if m, _ := d.Reg.ByLabel("demo-feature-x"); m.Port != 4101 {
		t.Fatal(m.Port)
	}
	site, _ := os.ReadFile(sitePath(t, d, "demo-feature-x"))
	if string(site) != "import worktree demo-feature-x.example.test 4101\n" {
		t.Fatalf("%q", site)
	}
	if err := os.Remove(filepath.Join(d.Reg.PortsDir, "4101")); err != nil {
		t.Fatal(err)
	}
	if out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err != nil || out.Manifest.Port != 4101 {
		t.Fatalf("link gone: %+v %v", out, err)
	}
}

// The port link must belong to the manifest's owner. Only root can create a
// link owned by someone else, so this runs as root only.
func TestGCRefusesPortLinkOfAnotherOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to chown the port link to another uid; CI runs it under sudo on Linux")
	}
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	if err := os.Lchown(filepath.Join(d.Reg.PortsDir, "4100"), nobody, -1); err != nil {
		t.Fatal(err)
	}
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || len(rep.Released) != 0 || !hasError(rep.Errors, fmt.Sprintf("port link 4100 is owned by uid %d", nobody)) {
		t.Fatalf("%+v %v", rep, err)
	}
}
