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
	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/testutil"
)

func TestUpPhoenixHappyPath(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	out, err := Up(context.Background(), d, c, UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := out.Manifest
	if m.Port != 4100 || m.Label != "demo-feature-x" || m.Host != "demo-feature-x.example.test" || !reflect.DeepEqual(m.DBBases, []string{"demo_dev", "demo_test"}) || m.DBSuffix != "_alice_feature_x" || m.Vhost != "" {
		t.Fatalf("%+v", m)
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != c.Worktree {
		t.Fatal(owner)
	}
	if _, err := os.Stat(sitePath(t, d, m.Label)); err != nil {
		t.Fatal(err)
	}
	env, _ := envfile.ReadEnvWorktree(filepath.Join(c.Worktree, ".env.worktree"))
	if !slices.Contains(env, envfile.KV{Key: "PORT", Value: "4100"}) || !slices.Contains(env, envfile.KV{Key: "PHX_HOST", Value: m.Host}) {
		t.Fatal(env)
	}
	calls := argvs(d.Run.(*execx.Fake))
	if !slices.Contains(calls, reloadCall) || !slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "sh -c mix setup") }) {
		t.Fatalf("%v", calls)
	}
	setup := lastCallWithPrefix(d.Run.(*execx.Fake), "sh")
	if !slices.Contains(setup.Env, "PORT=4100") || setup.Dir != c.Worktree {
		t.Fatalf("%+v", setup)
	}
}

func TestUpIsIdempotentAndKeepsSecret(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	first, _ := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	second, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || second.Manifest.Port != first.Manifest.Port || toMap(second.Env)["SECRET_KEY_BASE"] != toMap(first.Env)["SECRET_KEY_BASE"] {
		t.Fatal(err)
	}
	all, _, _ := d.Reg.All()
	if len(all) != 1 {
		t.Fatal(len(all))
	}
}

func TestUpReusesManifestByPathWhenNameChanges(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	first, _ := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	c.Name = "renamed"
	second, _ := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if second.Manifest.Label != first.Manifest.Label {
		t.Fatal(second.Manifest.Label)
	}
}

func TestUpLabelTakenByOtherWorktree(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	other := c
	other.Worktree = t.TempDir()
	testutil.CopyDir(t, filepath.Join("..", "preset", "testdata", "phoenix"), other.Worktree)
	_, err := Up(context.Background(), d, other, UpOptions{SkipSetup: true})
	var lt *ErrLabelTaken
	if !errors.As(err, &lt) || !strings.Contains(err.Error(), c.Worktree) {
		t.Fatal(err)
	}
}

// Another project with the same Mix app and worktree name would share the
// databases, and down there would drop them here.
func TestUpRefusesDatabasesOfAnotherWorktree(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	mustUp(t, d, c)
	other := secondWorktree(t, "phoenix")
	other.Name = c.Name
	writeFile(t, filepath.Join(other.Root, "hades.toml"), "project = \"shop\"\n")
	_, err := Up(context.Background(), d, other, UpOptions{SkipSetup: true})
	if err == nil || !strings.Contains(err.Error(), "database demo_dev_alice_feature_x is demo-feature-x's") {
		t.Fatal(err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal(len(all))
	}
}

func TestUpRollsBackWhenCaddyReloadFails(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	d.Run.(*execx.Fake).Handle = failWhen("caddy", 1)
	_, err := Up(context.Background(), d, c, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatal(err)
	}
	if _, err := d.Reg.PortOwner(4100); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("port not released")
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest kept")
	}
	if _, err := os.Stat(sitePath(t, d, "demo-feature-x")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("site kept")
	}
}

func TestUpKeepsStateWhenSetupFails(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	d.Run.(*execx.Fake).Handle = failWhen("sh", 1)
	_, err := Up(context.Background(), d, c, UpOptions{})
	var se *SetupError
	if !errors.As(err, &se) {
		t.Fatal(err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal("manifest removed")
	}
	if _, err := d.Reg.PortOwner(4100); err != nil {
		t.Fatal("port released")
	}
}

func TestUpNonePresetDoesNothing(t *testing.T) {
	d, c := newDeps(t, "none")
	out, err := Up(context.Background(), d, c, UpOptions{})
	if err != nil || out.Manifest != nil {
		t.Fatal(out, err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest for none")
	}
}

func TestUpWithDomainPublishesSiteAndReloads(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	d.Host.LabelTemplate = naming.DomainLabelTemplate
	writeFile(t, filepath.Join(c.Root, "hades.toml"), "project = \"shop\"\n")
	c.Name = "feat"
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || out.URL != "https://alice-shop-feat.example.test" {
		t.Fatalf("%+v %v", out, err)
	}
	if site, _ := os.ReadFile(filepath.Join(d.Reg.SitesDir, "alice-shop-feat.caddy")); string(site) != "import worktree alice-shop-feat.example.test 4100\n" {
		t.Fatalf("site file %q", site)
	}
	if calls := argvs(d.Run.(*execx.Fake)); !slices.Contains(calls, reloadCall) {
		t.Fatalf("%v", calls)
	}
}

func TestUpWithoutDomainUsesLocalhost(t *testing.T) {
	d, c := newLaptopDeps(t, "phoenix")
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || out.URL != "http://localhost:4100" {
		t.Fatalf("%+v %v", out, err)
	}
	if entries, _ := os.ReadDir(d.Reg.SitesDir); len(entries) != 0 {
		t.Fatalf("site files %v", entries)
	}
	if calls := argvs(d.Run.(*execx.Fake)); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "caddy") }) {
		t.Fatalf("%v", calls)
	}
}

func TestUpDryRunWritesNothing(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	var log bytes.Buffer
	d.Log = &log
	Up(context.Background(), d, c, UpOptions{DryRun: true})
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("wrote manifest in dry run")
	}
	if !strings.Contains(log.String(), "would") {
		t.Fatal(log.String())
	}
}

func TestUpInvalidNameFailsWithoutPanic(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	c.Name = "???"
	_, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err == nil || !strings.Contains(err.Error(), "???") {
		t.Fatal(err)
	}
	if all, _, _ := d.Reg.All(); len(all) != 0 {
		t.Fatal("manifest written for invalid name")
	}
}

func TestUpGitSecretFailureIsFatalWithRemedy(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	os.MkdirAll(filepath.Join(c.Worktree, ".gitsecret"), 0o755)
	d.Run.(*execx.Fake).Handle = failWhen("git", 1) // git secret reveal fails
	_, err := Up(context.Background(), d, c, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "git secret tell") {
		t.Fatal(err)
	}
}

// Tests beyond the brief.

func TestUpDryRunTouchesNoFiles(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	var log bytes.Buffer
	d.Log = &log
	mcpBefore, _ := os.ReadFile(filepath.Join(c.Worktree, ".mcp.json"))
	out, err := Up(context.Background(), d, c, UpOptions{DryRun: true})
	if err != nil || out.Manifest == nil || out.Manifest.Port != 4100 || toMap(out.Env)["PORT"] != "4100" {
		t.Fatalf("%+v %v", out, err)
	}
	for _, p := range []string{
		filepath.Join(c.Worktree, ".env.worktree"),
		filepath.Join(c.Worktree, ".envrc"),
		sitePath(t, d, "demo-feature-x"),
		filepath.Join(d.Reg.PortsDir, "4100"),
	} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists after dry run", p)
		}
	}
	if mcpAfter, _ := os.ReadFile(filepath.Join(c.Worktree, ".mcp.json")); !bytes.Equal(mcpBefore, mcpAfter) {
		t.Error(".mcp.json changed in dry run")
	}
	if calls := argvs(d.Run.(*execx.Fake)); len(calls) != 0 {
		t.Errorf("dry run reached the runner: %v", calls)
	}
	for _, want := range []string{"would claim port 4100", "would write manifest", "would run: " + reloadCall, "would run: sh -c mix setup"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

func TestUpRerunFailureKeepsEarlierState(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	if _, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err != nil {
		t.Fatal(err)
	}
	d.Run.(*execx.Fake).Handle = failWhen("caddy", 1)
	if _, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err == nil {
		t.Fatal("expected failure")
	}
	if all, _, _ := d.Reg.All(); len(all) != 1 {
		t.Fatal("earlier manifest removed")
	}
	if owner, _ := d.Reg.PortOwner(4100); owner != c.Worktree {
		t.Fatal("earlier port released")
	}
	if _, err := os.Stat(sitePath(t, d, "demo-feature-x")); err != nil {
		t.Fatal("earlier site removed")
	}
}

func TestUpVhostCreatedAndRolledBack(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	rmq := &fakeRabbit{}
	d.Rabbit = rmq
	d.Host.RabbitMQ = &config.RabbitMQ{AMQPURL: "amqp://u:p@127.0.0.1:5672"}
	writeFile(t, filepath.Join(c.Root, "hades.toml"), "project = \"demo\"\n[services.rabbitmq]\nvhost = true\n")
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || out.Manifest.Vhost != "demo-feature-x" || toMap(out.Env)["RABBITMQ_VHOST"] != "demo-feature-x" {
		t.Fatalf("%+v %v", out, err)
	}
	if len(rmq.ensured) != 1 || rmq.ensured[0] != "demo-feature-x" {
		t.Fatal(rmq.ensured)
	}

	d2, c2 := newDeps(t, "phoenix")
	rmq2 := &fakeRabbit{}
	d2.Rabbit = rmq2
	d2.Host.RabbitMQ = d.Host.RabbitMQ
	writeFile(t, filepath.Join(c2.Root, "hades.toml"), "project = \"demo\"\n[services.rabbitmq]\nvhost = true\n")
	d2.Run.(*execx.Fake).Handle = failWhen("caddy", 1)
	_, err = Up(context.Background(), d2, c2, UpOptions{SkipSetup: true})
	if err == nil || !strings.Contains(err.Error(), "delete vhost demo-feature-x") {
		t.Fatal(err)
	}
	if len(rmq2.deleted) != 1 || rmq2.deleted[0] != "demo-feature-x" {
		t.Fatal(rmq2.deleted)
	}
}

func TestUpVhostRequestedWithoutRabbitIsSkipped(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	var log bytes.Buffer
	d.Log = &log
	writeFile(t, filepath.Join(c.Root, "hades.toml"), "project = \"demo\"\n[services.rabbitmq]\nvhost = true\n")
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil || out.Manifest.Vhost != "" || toMap(out.Env)["RABBITMQ_VHOST"] != "" {
		t.Fatalf("%+v %v", out, err)
	}
	if !strings.Contains(log.String(), "vhost requested but this host has no rabbitmq; skipping") {
		t.Fatal(log.String())
	}
}

func TestUpDirenvAllowOnlyWhenInstalledAndNotFatal(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	if _, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(argvs(d.Run.(*execx.Fake)), func(c string) bool { return strings.HasPrefix(c, "direnv") }) {
		t.Fatal("direnv run although not installed")
	}

	d, c = newDeps(t, "phoenix")
	var log bytes.Buffer
	d.Log = &log
	d.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	d.Run.(*execx.Fake).Handle = failWhen("direnv", 1)
	if _, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argvs(d.Run.(*execx.Fake)), "direnv allow "+c.Worktree) || !strings.Contains(log.String(), "direnv allow") {
		t.Fatalf("%v\n%s", argvs(d.Run.(*execx.Fake)), log.String())
	}
}

func TestUpGitSecretRevealOnlyWithGitsecretDir(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if slices.Contains(argvs(d.Run.(*execx.Fake)), "git secret reveal -f") {
		t.Fatal("reveal without .gitsecret")
	}
	os.MkdirAll(filepath.Join(c.Worktree, ".gitsecret"), 0o755)
	Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if reveal := lastCallWithPrefix(d.Run.(*execx.Fake), "git secret reveal -f"); reveal.Dir != c.Worktree {
		t.Fatalf("%+v", reveal)
	}
}

func TestUpPresetErrorNamesWorktreeAndClaimsNothing(t *testing.T) {
	d, c := newDeps(t, "none")
	writeFile(t, filepath.Join(c.Worktree, "mix.exs"), "defmodule X do end\n")
	_, err := Up(context.Background(), d, c, UpOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), c.Worktree+": ") {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(d.Reg.PortsDir); len(entries) != 0 {
		t.Fatal("port claimed")
	}
}

func TestUpSetupEnvComputedWinsOverProcessEnv(t *testing.T) {
	t.Setenv("PORT", "1")
	d, c := newDeps(t, "phoenix")
	if _, err := Up(context.Background(), d, c, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	setup := lastCallWithPrefix(d.Run.(*execx.Fake), "sh")
	if slices.Contains(setup.Env, "PORT=1") || !slices.Contains(setup.Env, "PORT=4100") || !slices.Contains(setup.Env, "PATH="+os.Getenv("PATH")) {
		t.Fatalf("%v", setup.Env)
	}
}

func TestUpNonWebPresetGetsNoPortOrURL(t *testing.T) {
	d, c := newDeps(t, "go")
	// Tidewave asked for, but it needs a port.
	writeFile(t, filepath.Join(c.Root, "hades.toml"), "project = \"demo\"\n[mcp]\ntidewave = true\n")
	for run := 1; run <= 2; run++ { // the re-run must not trip over Port 0
		out, err := Up(context.Background(), d, c, UpOptions{})
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if out.URL != "" || out.Manifest.Port != 0 || out.Manifest.Host != "" {
			t.Fatalf("run %d: url=%q %+v", run, out.URL, out.Manifest)
		}
	}
	m, err := d.Reg.ByWorktree(c.Worktree)
	if err != nil || m.Port != 0 || m.Label != "demo-feature-x" {
		t.Fatalf("%+v %v", m, err)
	}
	if entries, _ := os.ReadDir(d.Reg.PortsDir); len(entries) != 0 {
		t.Fatal("port claimed for a non-web preset")
	}
	if _, err := os.Stat(sitePath(t, d, m.Label)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("site written for a non-web preset")
	}
	calls := argvs(d.Run.(*execx.Fake))
	if slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "caddy") }) || !slices.Contains(calls, "sh -c go mod download") {
		t.Fatalf("%v", calls)
	}
	env, _ := envfile.ReadEnvWorktree(filepath.Join(c.Worktree, ".env.worktree"))
	for _, e := range env {
		if slices.Contains([]string{"PORT", "PHX_HOST", "HADES_URL"}, e.Key) {
			t.Errorf(".env.worktree has %s", e.Key)
		}
	}
	if !slices.Contains(env, envfile.KV{Key: "HADES_LABEL", Value: "demo-feature-x"}) {
		t.Fatal(env)
	}
	if setup := lastCallWithPrefix(d.Run.(*execx.Fake), "sh"); slices.Contains(setup.Env, "HADES_URL=") {
		t.Fatalf("%v", setup.Env)
	}
	if mcp, _ := os.ReadFile(filepath.Join(c.Worktree, ".mcp.json")); !strings.Contains(string(mcp), "localhost:4000") {
		t.Fatalf("tidewave rewired without a port: %s", mcp)
	}
}

func TestUpNonePresetTouchesNoRegistry(t *testing.T) {
	d, c := newLaptopDeps(t, "none")
	d.Host.PortsDir = filepath.Join(t.TempDir(), "absent", "ports")
	d.Reg.PortsDir = d.Host.PortsDir
	c.Name = "???" // no label is derived either
	if out, err := Up(context.Background(), d, c, UpOptions{}); err != nil || out.Manifest != nil {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(d.Reg.PortsDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("registry created for none")
	}
}

// A re-run after a config change keeps what earlier runs recorded: the bases
// only grow and a vhost no longer asked for stays recorded, so down still
// releases everything the first run created.
func TestUpRerunKeepsRecordedBasesAndVhost(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	rmq := &fakeRabbit{}
	d.Rabbit = rmq
	d.Host.RabbitMQ = &config.RabbitMQ{AMQPURL: "amqp://u:p@127.0.0.1:5672"}
	writeFile(t, filepath.Join(c.Root, "hades.toml"), "project = \"demo\"\n[services.rabbitmq]\nvhost = true\n")
	mustUp(t, d, c)

	writeFile(t, filepath.Join(c.Worktree, "hades.toml"), "databases = [\"only_this\", \"demo_test\"]\n[services.rabbitmq]\nvhost = false\n")
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.Reg.ByLabel("demo-feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"demo_dev", "demo_test", "only_this"}; !reflect.DeepEqual(m.DBBases, want) || !reflect.DeepEqual(out.Manifest.DBBases, want) {
		t.Fatalf("bases %v, want %v", m.DBBases, want)
	}
	if m.Vhost != "demo-feature-x" {
		t.Fatalf("vhost %q", m.Vhost)
	}
	// Not asked for any more: not ensured again and not in the environment.
	if len(rmq.ensured) != 1 || toMap(out.Env)["RABBITMQ_VHOST"] != "" {
		t.Fatalf("ensured %v env %v", rmq.ensured, toMap(out.Env))
	}

	d.PG.(*fakePG).names = []string{"demo_dev_alice_feature_x", "only_this_alice_feature_x"}
	if _, err := Down(context.Background(), d, c, true); err != nil {
		t.Fatal(err)
	}
	if got := d.PG.(*fakePG).dropped; !reflect.DeepEqual(got, []string{"demo_dev_alice_feature_x", "only_this_alice_feature_x"}) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(rmq.deleted, []string{"demo-feature-x"}) {
		t.Fatal(rmq.deleted)
	}
}

func TestUnion(t *testing.T) {
	if got := union([]string{"a", "b"}, []string{"c", "a", "c"}); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatal(got)
	}
	if got := union(nil, nil); len(got) != 0 {
		t.Fatal(got)
	}
}

// project from hades.toml is a slug like the directory-derived one.
func TestUpProjectFromConfigIsSlugged(t *testing.T) {
	d, c := newDeps(t, "phoenix")
	writeFile(t, filepath.Join(c.Root, "hades.toml"), "project = \"My Demo App\"\n[env]\nP = \"{{project}}\"\n")
	out, err := Up(context.Background(), d, c, UpOptions{SkipSetup: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Manifest.Project != "my-demo-app" || out.Manifest.Label != "my-demo-app-feature-x" || toMap(out.Env)["P"] != "my-demo-app" {
		t.Fatalf("%+v %v", out.Manifest, toMap(out.Env))
	}

	d2, c2 := newDeps(t, "phoenix")
	long := "My Very Long Project Name That Exceeds Thirty Characters!!"
	writeFile(t, filepath.Join(c2.Root, "hades.toml"), "project = \""+long+"\"\n")
	if _, err := Up(context.Background(), d2, c2, UpOptions{SkipSetup: true}); err == nil || !strings.Contains(err.Error(), long) || !strings.Contains(err.Error(), "30 characters") {
		t.Fatal(err)
	}
	if all, _, _ := d2.Reg.All(); len(all) != 0 {
		t.Fatal("claimed something for an invalid project")
	}
	writeFile(t, filepath.Join(c2.Root, "hades.toml"), "project = \"!!!\"\n")
	if _, err := Up(context.Background(), d2, c2, UpOptions{SkipSetup: true}); err == nil || !strings.Contains(err.Error(), `"!!!"`) {
		t.Fatal(err)
	}
}

// After a down that kept the manifest, the next up in the same worktree
// claims a fresh port instead of failing on the released one.
func TestUpAfterKeptManifest(t *testing.T) {
	d, c := newDeps(t, "phoenix")
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
// port whose link is gone or is another worktree's; up claims a new one.
func TestUpReclaimsPortWhenRecordedLinkIsGoneOrForeign(t *testing.T) {
	d, c := newDeps(t, "phoenix")
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
