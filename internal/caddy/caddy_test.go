package caddy_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/execx"
)

func TestWriteAndRemoveSite(t *testing.T) {
	dir := t.TempDir()
	if err := caddy.WriteSite(dir, "shop-x", "shop-x.example.test", 4100); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "shop-x.caddy"))
	if string(b) != "import worktree shop-x.example.test 4100\n" {
		t.Fatalf("%q", b)
	}
	if err := caddy.WriteSite(dir, "x", "bad host", 1); err == nil {
		t.Fatal("bad host accepted")
	}
	if err := caddy.RemoveSite(dir, "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestReloadRunsTheFixedCommand(t *testing.T) {
	f := &execx.Fake{}
	err := caddy.Reload(context.Background(), f)
	if err != nil || len(f.Calls) != 1 || !reflect.DeepEqual(f.Calls[0].Argv, []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile"}) {
		t.Fatalf("%+v %v", f.Calls, err)
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

func TestSitePathAndRemoveSiteRejectBadLabel(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sites")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(base, "victim.caddy")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"", "../victim", "a/b", ".", ".."} {
		if p, err := caddy.SitePath(dir, l); err == nil {
			t.Errorf("SitePath(%q) = %q", l, p)
		}
		if caddy.RemoveSite(dir, l) == nil {
			t.Errorf("RemoveSite(%q) accepted", l)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("RemoveSite removed a file outside the sites directory")
	}
	if p, err := caddy.SitePath(dir, "ok"); err != nil || p != filepath.Join(dir, "ok.caddy") {
		t.Fatal(p, err)
	}
}
