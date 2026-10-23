package caddy_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/execx"
)

func TestWriteValidateRemove(t *testing.T) {
	dir := t.TempDir()
	if err := caddy.WriteSite(dir, "jarl-x", "jarl-x.example.test", 4100); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(caddy.SitePath(dir, "jarl-x"))
	if string(b) != "import worktree jarl-x.example.test 4100\n" {
		t.Fatalf("%q", b)
	}
	if err := caddy.ValidateSites(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "evil.caddy"), []byte("reverse_proxy 10.0.0.1:22\n"), 0o644)
	err := caddy.ValidateSites(dir)
	if err == nil || !strings.Contains(err.Error(), "evil.caddy") {
		t.Fatal(err)
	}
	if err := caddy.WriteSite(dir, "x", "bad host", 1); err == nil {
		t.Fatal("bad host accepted")
	}
	if err := caddy.RemoveSite(dir, "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestReloadRunsValidateThenReload(t *testing.T) {
	dir := t.TempDir()
	caddy.WriteSite(dir, "a", "a.example.test", 4100)
	f := &execx.Fake{}
	cfg := config.Caddy{Config: "/etc/caddy/Caddyfile", LiveDir: t.TempDir(), Reload: []string{"systemctl", "reload", "caddy"}}
	if err := caddy.Reload(context.Background(), cfg, dir, false, f); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 2 || f.Calls[0].Argv[0] != "caddy" || f.Calls[0].Argv[1] != "validate" || f.Calls[1].Argv[2] != "caddy" {
		t.Fatalf("%+v", f.Calls)
	}
	f = &execx.Fake{}
	caddy.Reload(context.Background(), cfg, dir, true, f)
	if len(f.Calls) != 1 {
		t.Fatal("check must not reload")
	}
	os.WriteFile(filepath.Join(dir, "bad.caddy"), []byte("x\n"), 0o644)
	f = &execx.Fake{}
	if err := caddy.Reload(context.Background(), cfg, dir, false, f); err == nil || len(f.Calls) != 0 {
		t.Fatal("ran caddy despite bad site file")
	}
}

func TestSudoReloadArgv(t *testing.T) {
	f := &execx.Fake{}
	caddy.SudoReload(context.Background(), f, "/usr/local/bin/hades")
	if !reflect.DeepEqual(f.Calls[0].Argv, []string{"sudo", "-n", "/usr/local/bin/hades", "caddy-reload"}) {
		t.Fatal(f.Calls[0].Argv)
	}
}

func readDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range es {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		m[e.Name()] = string(b)
	}
	return m
}

func TestReloadMirrorsToLiveDir(t *testing.T) {
	dir, live := t.TempDir(), t.TempDir()
	caddy.WriteSite(dir, "a", "a.example.test", 4100)
	caddy.WriteSite(dir, "b", "b.example.test", 4101)
	os.WriteFile(filepath.Join(live, "stale.caddy"), []byte("import worktree s.example.test 1\n"), 0o644)
	os.WriteFile(filepath.Join(live, "keep.txt"), []byte("x"), 0o644)
	cfg := config.Caddy{Config: "c", LiveDir: live, Reload: []string{"r"}}
	f := &execx.Fake{}
	if err := caddy.Reload(context.Background(), cfg, dir, false, f); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"a.caddy":  "import worktree a.example.test 4100\n",
		"b.caddy":  "import worktree b.example.test 4101\n",
		"keep.txt": "x",
	}
	if got := readDir(t, live); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	if len(f.Calls) != 2 || !reflect.DeepEqual(f.Calls[0].Env, env) || !reflect.DeepEqual(f.Calls[1].Env, env) {
		t.Fatalf("%+v", f.Calls)
	}
}

func TestReloadInvalidOrCheckLeavesLiveDirAlone(t *testing.T) {
	dir, live := t.TempDir(), t.TempDir()
	caddy.WriteSite(dir, "a", "a.example.test", 4100)
	os.WriteFile(filepath.Join(live, "old.caddy"), []byte("import worktree o.example.test 1\n"), 0o644)
	before := readDir(t, live)
	cfg := config.Caddy{Config: "c", LiveDir: live, Reload: []string{"r"}}

	f := &execx.Fake{}
	if err := caddy.Reload(context.Background(), cfg, dir, true, f); err != nil || !reflect.DeepEqual(readDir(t, live), before) {
		t.Fatal("check touched LiveDir", err)
	}
	os.WriteFile(filepath.Join(dir, "bad.caddy"), []byte("x\n"), 0o644)
	f = &execx.Fake{}
	if err := caddy.Reload(context.Background(), cfg, dir, false, f); err == nil || len(f.Calls) != 0 || !reflect.DeepEqual(readDir(t, live), before) {
		t.Fatal("invalid sites affected LiveDir or ran commands")
	}
}

func TestWriteSiteRejectsBadLabelAndPort(t *testing.T) {
	dir := t.TempDir()
	for _, l := range []string{"", "../x", "a/b", ".", ".."} {
		if caddy.WriteSite(dir, l, "h.test", 1) == nil {
			t.Errorf("label %q accepted", l)
		}
	}
	for _, p := range []int{0, -1, 65536} {
		if caddy.WriteSite(dir, "ok", "h.test", p) == nil {
			t.Errorf("port %d accepted", p)
		}
	}
}

func TestValidateRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	big := "import worktree a.test " + strings.Repeat("1", 300) + "\n"
	os.WriteFile(filepath.Join(dir, "big.caddy"), []byte(big), 0o644)
	if caddy.ValidateSites(dir) == nil {
		t.Fatal("oversized file accepted")
	}
}
