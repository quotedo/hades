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
	cfg := config.Caddy{Config: "/etc/caddy/Caddyfile", Reload: []string{"systemctl", "reload", "caddy"}}
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
