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

// newHost is a host config whose sites, manifests and live directories are
// fresh temp dirs, with domain example.test.
func newHost(t *testing.T) *config.Host {
	t.Helper()
	base := t.TempDir()
	h := &config.Host{
		Domain:       "example.test",
		SitesDir:     filepath.Join(base, "sites"),
		ManifestsDir: filepath.Join(base, "manifests"),
		Caddy:        config.Caddy{Config: "c", LiveDir: filepath.Join(base, "live"), Reload: []string{"r"}},
	}
	for _, d := range []string{h.SitesDir, h.ManifestsDir, h.Caddy.LiveDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// addSite writes the site file of label, for <label>.example.test, and the
// manifest file that authorises it.
func addSite(t *testing.T, h *config.Host, label string, port int) {
	t.Helper()
	if err := caddy.WriteSite(h.SitesDir, label, label+"."+h.Domain, port); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.ManifestsDir, label+".toml"), []byte("label = \""+label+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteValidateRemove(t *testing.T) {
	h := newHost(t)
	dir := h.SitesDir
	addSite(t, h, "shop-x", 4100)
	b, _ := os.ReadFile(filepath.Join(dir, "shop-x.caddy"))
	if string(b) != "import worktree shop-x.example.test 4100\n" {
		t.Fatalf("%q", b)
	}
	if err := caddy.ValidateSites(h); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "evil.caddy"), []byte("reverse_proxy 10.0.0.1:22\n"), 0o644)
	err := caddy.ValidateSites(h)
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
	h := newHost(t)
	dir := h.SitesDir
	addSite(t, h, "a", 4100)
	f := &execx.Fake{}
	h.Caddy.Config, h.Caddy.Reload = "/etc/caddy/Caddyfile", []string{"systemctl", "reload", "caddy"}
	if err := caddy.Reload(context.Background(), h, false, f); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 2 || f.Calls[0].Argv[0] != "caddy" || f.Calls[0].Argv[1] != "validate" || f.Calls[1].Argv[2] != "caddy" {
		t.Fatalf("%+v", f.Calls)
	}
	f = &execx.Fake{}
	caddy.Reload(context.Background(), h, true, f)
	if len(f.Calls) != 1 {
		t.Fatal("check must not reload")
	}
	os.WriteFile(filepath.Join(dir, "bad.caddy"), []byte("x\n"), 0o644)
	f = &execx.Fake{}
	if err := caddy.Reload(context.Background(), h, false, f); err == nil || len(f.Calls) != 0 {
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
	h := newHost(t)
	live := h.Caddy.LiveDir
	addSite(t, h, "a", 4100)
	addSite(t, h, "b", 4101)
	os.WriteFile(filepath.Join(live, "stale.caddy"), []byte("import worktree s.example.test 1\n"), 0o644)
	os.WriteFile(filepath.Join(live, "keep.txt"), []byte("x"), 0o644)
	f := &execx.Fake{}
	if err := caddy.Reload(context.Background(), h, false, f); err != nil {
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
	h := newHost(t)
	dir, live := h.SitesDir, h.Caddy.LiveDir
	addSite(t, h, "a", 4100)
	os.WriteFile(filepath.Join(live, "old.caddy"), []byte("import worktree o.example.test 1\n"), 0o644)
	before := readDir(t, live)

	f := &execx.Fake{}
	if err := caddy.Reload(context.Background(), h, true, f); err != nil || !reflect.DeepEqual(readDir(t, live), before) {
		t.Fatal("check touched LiveDir", err)
	}
	os.WriteFile(filepath.Join(dir, "bad.caddy"), []byte("x\n"), 0o644)
	f = &execx.Fake{}
	if err := caddy.Reload(context.Background(), h, false, f); err == nil || len(f.Calls) != 0 || !reflect.DeepEqual(readDir(t, live), before) {
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

func TestValidateRejectsOversizedFile(t *testing.T) {
	h := newHost(t)
	big := "import worktree big.example.test " + strings.Repeat("1", 300) + "\n"
	os.WriteFile(filepath.Join(h.SitesDir, "big.caddy"), []byte(big), 0o644)
	os.WriteFile(filepath.Join(h.ManifestsDir, "big.toml"), nil, 0o644)
	if caddy.ValidateSites(h) == nil {
		t.Fatal("oversized file accepted")
	}
}

// A site file may publish only <label>.<domain>, and only for a label whose
// manifest its author owns; anything else refuses the whole reload, naming
// the file, before a live change or a command.
func TestReloadRefusesUnauthorisedSites(t *testing.T) {
	for _, c := range []struct {
		name, want string
		plant      func(t *testing.T, h *config.Host)
	}{
		{"foreign host", "host admin.example.test is not x.example.test", func(t *testing.T, h *config.Host) {
			os.WriteFile(filepath.Join(h.SitesDir, "x.caddy"), []byte("import worktree admin.example.test 4200\n"), 0o644)
			os.WriteFile(filepath.Join(h.ManifestsDir, "x.toml"), nil, 0o644)
		}},
		{"another label's host", "host a.example.test is not x.example.test", func(t *testing.T, h *config.Host) {
			os.WriteFile(filepath.Join(h.SitesDir, "x.caddy"), []byte("import worktree a.example.test 4200\n"), 0o644)
			os.WriteFile(filepath.Join(h.ManifestsDir, "x.toml"), nil, 0o644)
		}},
		{"other domain", "host x.evil.test is not x.example.test", func(t *testing.T, h *config.Host) {
			os.WriteFile(filepath.Join(h.SitesDir, "x.caddy"), []byte("import worktree x.evil.test 4200\n"), 0o644)
			os.WriteFile(filepath.Join(h.ManifestsDir, "x.toml"), nil, 0o644)
		}},
		{"no manifest", "no such file or directory", func(t *testing.T, h *config.Host) {
			caddy.WriteSite(h.SitesDir, "x", "x.example.test", 4200)
		}},
		{"symlinked manifest", "is not a regular file", func(t *testing.T, h *config.Host) {
			caddy.WriteSite(h.SitesDir, "x", "x.example.test", 4200)
			target := filepath.Join(t.TempDir(), "x.toml")
			os.WriteFile(target, nil, 0o644)
			os.Symlink(target, filepath.Join(h.ManifestsDir, "x.toml"))
		}},
		{"manifest directory", "is not a regular file", func(t *testing.T, h *config.Host) {
			caddy.WriteSite(h.SitesDir, "x", "x.example.test", 4200)
			os.Mkdir(filepath.Join(h.ManifestsDir, "x.toml"), 0o755)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			addSite(t, h, "a", 4100)
			c.plant(t, h)
			before := readDir(t, h.Caddy.LiveDir)
			f := &execx.Fake{}
			err := caddy.Reload(context.Background(), h, false, f)
			if err == nil || !strings.Contains(err.Error(), filepath.Join(h.SitesDir, "x.caddy")) || !strings.Contains(err.Error(), c.want) {
				t.Fatal(err)
			}
			if len(f.Calls) != 0 || !reflect.DeepEqual(readDir(t, h.Caddy.LiveDir), before) {
				t.Fatal("refused reload ran a command or changed the live dir")
			}
		})
	}
}

// The manifest must be owned by the site file's owner. Only root can make
// them differ.
func TestReloadRefusesSiteWhoseManifestHasAnotherOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to chown the manifest to another uid; CI runs it under sudo on Linux")
	}
	const nobody = 65534
	h := newHost(t)
	addSite(t, h, "x", 4100)
	if err := os.Chown(filepath.Join(h.ManifestsDir, "x.toml"), nobody, -1); err != nil {
		t.Fatal(err)
	}
	if err := caddy.ValidateSites(h); err == nil || !strings.Contains(err.Error(), "by uid 65534") {
		t.Fatal(err)
	}
}

func TestReloadRequiresDomain(t *testing.T) {
	h := newHost(t)
	addSite(t, h, "a", 4100)
	h.Domain = ""
	if err := caddy.ValidateSites(h); err == nil || !strings.Contains(err.Error(), "no domain") {
		t.Fatal(err)
	}
}
