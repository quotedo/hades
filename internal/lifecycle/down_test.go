package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/wtctx"
)

func TestDownRemovesEverythingItOwns(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	d.PG.(*fakePG).names = []string{"demo_dev_roman_feature_x", "demo_test_roman_feature_x1", "demo_dev_roman_other"}
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	res, err := Down(context.Background(), d, c, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Err != nil || r.Skipped {
			t.Fatalf("%+v", r)
		}
	}
	if got := d.PG.(*fakePG).dropped; !reflect.DeepEqual(got, []string{"demo_dev_roman_feature_x", "demo_test_roman_feature_x1"}) {
		t.Fatal(got)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest left")
	}
	if _, err := d.Reg.PortOwner(4100); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("port left")
	}
}

func TestDownWithoutManifestExitsCleanly(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	res, err := Down(context.Background(), d, c, true)
	if err != nil || len(res) != 1 || !res[0].Skipped {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDownRefusesOtherUsersManifest(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	d.User.UID = 999
	res, _ := Down(context.Background(), d, c, false)
	if !res[0].Skipped || !strings.Contains(res[0].Step, "manifest") {
		t.Fatalf("%+v", res)
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal("touched another user's manifest")
	}
}

func TestDownUsesManifestBasesNotCurrentConfig(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	os.WriteFile(filepath.Join(c.Worktree, "hades.toml"), []byte("databases = [\"other\"]\n"), 0o644)
	d.PG.(*fakePG).names = []string{"demo_dev_roman_feature_x", "other_dev_roman_feature_x"}
	Down(context.Background(), d, c, false)
	if got := d.PG.(*fakePG).dropped; !reflect.DeepEqual(got, []string{"demo_dev_roman_feature_x"}) {
		t.Fatal(got)
	}
}

func TestDownBestEffortAndStrict(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	d.PG.(*fakePG).failDrop = true
	d.PG.(*fakePG).names = []string{"demo_dev_roman_feature_x"}
	var log bytes.Buffer
	d.Log = &log
	res, err := Down(context.Background(), d, c, false)
	if err != nil {
		t.Fatal("best effort must not error")
	}
	// The other steps still ran, but the manifest stays as the record of the
	// databases that are left, for ls to show and gc to retry.
	if _, err := d.Reg.PortOwner(4100); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("port left")
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal("manifest removed although a step failed")
	}
	if r := res[len(res)-1]; r.Step != "manifest" || !r.Skipped || r.Reason != "kept (failed: databases); gc will retry" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(log.String(), "down: manifest kept (failed: databases); gc will retry\n") {
		t.Fatal(log.String())
	}
	// A strict down fails; once the cause is fixed a plain down finishes.
	if _, err := Down(context.Background(), d, c, true); err == nil {
		t.Fatal("strict must error")
	}
	d.PG.(*fakePG).failDrop = false
	if res, err := Down(context.Background(), d, c, true); err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest left after a successful down")
	}
}

func TestGCReleasesVanishedWorktreesAndOwner(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	d2, c2 := newServerDepsSharing(t, d, "phoenix", "roman") // second worktree, same registry and user
	Up(context.Background(), d2, c2, UpOptions{SkipSetup: true})
	os.RemoveAll(c.Worktree)
	rep, _ := GC(context.Background(), d, "", false)
	if len(rep.Released) != 1 || rep.Released[0] != "demo-feature-x" {
		t.Fatalf("%+v", rep)
	}
	rep, _ = GC(context.Background(), d, "roman", false)
	if len(rep.Released) != 1 {
		t.Fatalf("%+v", rep)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal(len(all))
	}
}

// gc --owner selects by the manifest file's owner, never by its user field.
func TestGCOwnerSelectsByFileOwner(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	d2, c2 := newServerDepsSharing(t, d, "phoenix", "other") // user field "other", file owned by roman
	mustUp(t, d2, c2)
	rep, err := GC(context.Background(), d, "other", false)
	if err != nil || len(rep.Released) != 0 || len(rep.Errors) != 0 {
		t.Fatalf("released by the user field: %+v %v", rep, err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 2 {
		t.Fatal(len(all))
	}
	if _, err := GC(context.Background(), d, "nobody", false); err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Fatal(err)
	}
	// roman's run selects both files roman owns, and then verification
	// refuses the one whose suffix was computed for "other".
	rep, err = GC(context.Background(), d, "roman", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x"}) || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error(), "refusing ") {
		t.Fatalf("%+v %v", rep, err)
	}
}

// mustUp runs Up without setup and fails the test on error.
func mustUp(t *testing.T, d Deps, c wtctx.Context) {
	t.Helper()
	if _, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err != nil {
		t.Fatal(err)
	}
}

func findStep(res []StepResult, step string) (StepResult, bool) {
	for _, r := range res {
		if r.Step == step {
			return r, true
		}
	}
	return StepResult{}, false
}

func countCalls(f *execx.Fake, want string) int {
	n := 0
	for _, c := range argvs(f) {
		if c == want {
			n++
		}
	}
	return n
}

const sudoReload = "sudo -n /usr/local/bin/hades caddy-reload"

func TestDownRunsTeardownInWorktreeWithItsEnv(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	writeFile(t, filepath.Join(c.Worktree, "hades.toml"), "teardown = [\"mix ecto.drop\", \"echo bye\"]\n")
	if _, err := Down(context.Background(), d, c, true); err != nil {
		t.Fatal(err)
	}
	f := d.Run.(*execx.Fake)
	calls := argvs(f)
	if !hasCall(calls, "sh -c mix ecto.drop") || !hasCall(calls, "sh -c echo bye") {
		t.Fatalf("%v", calls)
	}
	td := lastCallWithPrefix(f, "sh -c mix ecto.drop")
	if td.Dir != c.Worktree || !envHas(td.Env, "PORT=4100") || !envHas(td.Env, "PHX_HOST=demo-feature-x.example.test") {
		t.Fatalf("%+v", td)
	}
}

func TestDownTeardownFailureIsReportedAndOthersStillRun(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	writeFile(t, filepath.Join(c.Worktree, "hades.toml"), "teardown = [\"false\", \"echo after\"]\n")
	var log bytes.Buffer
	d.Log = &log
	d.Run.(*execx.Fake).Handle = func(cmd execx.Cmd) (execx.Result, error) {
		if strings.Join(cmd.Argv, " ") == "sh -c false" {
			res := execx.Result{ExitCode: 1}
			return res, &execx.ExitError{Cmd: cmd, Result: res}
		}
		return execx.Result{}, nil
	}
	res, err := Down(context.Background(), d, c, true)
	if err == nil {
		t.Fatal("strict must error")
	}
	if r, _ := findStep(res, "teardown"); r.Err == nil {
		t.Fatalf("%+v", res)
	}
	if !hasCall(argvs(d.Run.(*execx.Fake)), "sh -c echo after") {
		t.Fatal("later teardown command did not run")
	}
	if !strings.Contains(log.String(), "down: failed teardown: ") || !strings.Contains(log.String(), "down: manifest kept (failed: teardown)") {
		t.Fatal(log.String())
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal("manifest removed although teardown failed")
	}
}

func TestDownSkipsTeardownWhenWorktreeIsGone(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	d.Log = &log
	res, err := Down(context.Background(), d, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := findStep(res, "teardown"); !ok || !r.Skipped || r.Reason == "" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(log.String(), "down: skipped teardown: ") {
		t.Fatal(log.String())
	}
	if _, err := d.Reg.PortOwner(4100); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("port left")
	}
}

func TestDownRemovesSiteAndReloadsCaddy(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	f := d.Run.(*execx.Fake)
	f.Calls = nil
	if _, err := Down(context.Background(), d, c, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sitePath(t, d, "demo-feature-x")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("site left")
	}
	if countCalls(f, sudoReload) != 1 {
		t.Fatalf("%v", argvs(f))
	}
}

func TestDownDeletesVhostOrSkipsWithoutRabbit(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	rmq := &fakeRabbit{}
	d.Rabbit = rmq
	d.Host.RabbitMQ = &config.RabbitMQ{AMQPURL: "amqp://u:p@127.0.0.1:5672"}
	writeFile(t, filepath.Join(c.Worktree, "hades.toml"), "project = \"demo\"\n[services.rabbitmq]\nvhost = true\n")
	mustUp(t, d, c)
	if _, err := Down(context.Background(), d, c, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rmq.deleted, []string{"demo-feature-x"}) {
		t.Fatal(rmq.deleted)
	}

	mustUp(t, d, c)
	d.Rabbit = nil
	res, err := Down(context.Background(), d, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := findStep(res, "vhost"); !ok || !r.Skipped {
		t.Fatalf("%+v", res)
	}
}

func TestDownAsRootActsOnAnyManifestButSkipsTeardown(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the manifest would be root's own")
	}
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	writeFile(t, filepath.Join(c.Worktree, "hades.toml"), "teardown = [\"mix ecto.drop\"]\n")
	d.User.UID = 0
	res, err := Down(context.Background(), d, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest left")
	}
	if r, ok := findStep(res, "teardown"); !ok || !r.Skipped || r.Reason != "teardown runs only as the owner; use gc" {
		t.Fatalf("%+v", res)
	}
	if hasCallPrefix(argvs(d.Run.(*execx.Fake)), "sh -c mix ecto.drop") {
		t.Fatal("ran another user's teardown as root")
	}
}

func TestDownNeverDropsOtherWorktreesDatabases(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	d.PG.(*fakePG).names = []string{
		"demo_dev_roman_feature_x",
		"demo_test_roman_feature_x2",   // this worktree's MIX_TEST_PARTITION=2
		"demo_dev_roman_feature_x1_",   // roman's worktree feature/x1
		"demo_dev_bob_roman_feature_x", // bob's worktree roman-feature-x
		"demoapp_dev_roman_feature_x",  // another project
	}
	if _, err := Down(context.Background(), d, c, true); err != nil {
		t.Fatal(err)
	}
	if got := d.PG.(*fakePG).dropped; !reflect.DeepEqual(got, []string{"demo_dev_roman_feature_x", "demo_test_roman_feature_x2"}) {
		t.Fatal(got)
	}
}

func TestDownWithoutDatabaseBasesIsSilent(t *testing.T) {
	d, c := newServerDeps(t, "go")
	mustUp(t, d, c)
	var log bytes.Buffer
	d.Log = &log
	res, err := Down(context.Background(), d, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findStep(res, "databases"); ok || log.Len() != 0 {
		t.Fatalf("%+v %q", res, log.String())
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest left")
	}
}

// A port link pointing at another worktree is nothing to release: a skip,
// and the other worktree's claim stays.
func TestDownPortClaimedByAnotherWorktreeIsASkip(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	link := filepath.Join(d.Reg.PortsDir, "4100")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	res, err := Down(context.Background(), d, c, true)
	if r, _ := findStep(res, "port"); err != nil || r.Err != nil || !r.Skipped || !strings.Contains(r.Reason, "is now claimed by "+other) {
		t.Fatalf("%+v %v", res, err)
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != other {
		t.Fatal("released another worktree's port")
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest left")
	}
}

func TestDownOnLaptopWithoutRegistrySkips(t *testing.T) {
	d, c := newLaptopDeps(t, "phoenix")
	if err := os.RemoveAll(d.Reg.ManifestsDir); err != nil {
		t.Fatal(err)
	}
	res, err := Down(context.Background(), d, c, true)
	if err != nil || len(res) != 1 || res[0].Step != "manifest" || !res[0].Skipped {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGCDryRunChangesNothing(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	f := d.Run.(*execx.Fake)
	f.Calls = nil
	rep, err := GC(context.Background(), d, "", true)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x"}) || len(rep.Errors) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal("dry run removed the manifest")
	}
	if _, err := d.Reg.PortOwner(4100); err != nil {
		t.Fatal("dry run released the port")
	}
	if len(f.Calls) != 0 {
		t.Fatalf("%v", argvs(f))
	}
}

func TestGCReloadsCaddyOnceForSeveralSites(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	d2, c2 := newServerDepsSharing(t, d, "phoenix", "roman")
	mustUp(t, d2, c2)
	for _, wt := range []string{c.Worktree, c2.Worktree} {
		if err := os.RemoveAll(wt); err != nil {
			t.Fatal(err)
		}
	}
	f := d.Run.(*execx.Fake)
	f.Calls = nil
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x", "demo-feature-y"}) || len(rep.Errors) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if countCalls(f, sudoReload) != 1 {
		t.Fatalf("%v", argvs(f))
	}
}

func TestGCRemovesStalePortLinks(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.RemoveAll(c.Worktree); err != nil { // referenced by a manifest: released with it
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	alive := t.TempDir()
	for port, target := range map[string]string{"4110": gone, "4111": alive} {
		if err := os.Symlink(target, filepath.Join(d.Reg.PortsDir, port)); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x", "port 4110"}) || len(rep.Errors) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := d.Reg.PortOwner(4110); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("stale link left")
	}
	if owner, _ := d.Reg.PortOwner(4111); owner != alive {
		t.Fatal("removed a live link")
	}
}

func TestGCReportsStepErrorsAndCarriesOn(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	d2, c2 := newServerDepsSharing(t, d, "phoenix", "roman")
	mustUp(t, d2, c2)
	d.PG.(*fakePG).names = []string{"demo_dev_roman_feature_x"}
	d.PG.(*fakePG).failDrop = true
	for _, wt := range []string{c.Worktree, c2.Worktree} {
		if err := os.RemoveAll(wt); err != nil {
			t.Fatal(err)
		}
	}
	var log bytes.Buffer
	d.Log = &log
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-y"}) || len(rep.Errors) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if msg := rep.Errors[0].Error(); !strings.Contains(msg, "demo-feature-x") || !strings.Contains(msg, "databases") {
		t.Fatal(msg)
	}
	if !strings.Contains(log.String(), "gc: demo-feature-x: manifest kept (failed: databases)") {
		t.Fatal(log.String())
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 || all[0].Label != "demo-feature-x" {
		t.Fatal("the failed manifest must stay for the next gc")
	}
	// The next gc retries, and finds the port already released.
	d.PG.(*fakePG).failDrop = false
	rep, err = GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x"}) || len(rep.Errors) != 0 {
		t.Fatalf("retry: %+v %v", rep, err)
	}
	if !reflect.DeepEqual(d.PG.(*fakePG).dropped, []string{"demo_dev_roman_feature_x"}) {
		t.Fatal(d.PG.(*fakePG).dropped)
	}
}

func TestGCOnLaptopLeavesOtherUsersManifests(t *testing.T) {
	d, c := newLaptopDeps(t, "phoenix")
	mustUp(t, d, c)
	d2, c2 := newServerDepsSharing(t, d, "phoenix", "other")
	mustUp(t, d2, c2)
	for _, wt := range []string{c.Worktree, c2.Worktree} {
		if err := os.RemoveAll(wt); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := GC(context.Background(), d, "", false)
	if err != nil || !reflect.DeepEqual(rep.Released, []string{"demo-feature-x"}) {
		t.Fatalf("%+v %v", rep, err)
	}
	all, _, _ := d.Reg.All()
	if !slices.ContainsFunc(all, func(m *registry.Manifest) bool { return m.User == "other" }) {
		t.Fatal("released another user's manifest")
	}
}

// A port link that is already gone is a skip, so it cannot keep the manifest.
func TestDownMissingPortLinkIsASkip(t *testing.T) {
	d, c := newServerDeps(t, "phoenix")
	mustUp(t, d, c)
	if err := os.Remove(filepath.Join(d.Reg.PortsDir, "4100")); err != nil {
		t.Fatal(err)
	}
	res, err := Down(context.Background(), d, c, true)
	if err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if r, ok := findStep(res, "port"); !ok || !r.Skipped || r.Reason != "port link 4100 is already gone" {
		t.Fatalf("%+v", res)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest left")
	}
}
