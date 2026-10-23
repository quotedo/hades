package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// laptopUpFixture lays out a root checkout named demo and a worktree holding
// the given preset fixture, so no git is needed when both are passed as flags.
func laptopUpFixture(t *testing.T, fixture string) (root, wt string, getenv func(string) string) {
	t.Helper()
	tmp := t.TempDir()
	root, wt = filepath.Join(tmp, "demo"), filepath.Join(tmp, "wt")
	for _, d := range []string{root, wt} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join("..", "preset", "testdata", fixture)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(tmp, "home")
	getenv = func(k string) string {
		switch k {
		case "HADES_HOST_CONFIG":
			return "laptop"
		case "HOME":
			return home
		}
		return ""
	}
	return root, wt, getenv
}

func TestUpDryRunThroughDispatcher(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "phoenix")
	var out, errb bytes.Buffer
	code := Main([]string{"--root", root, "--worktree", wt, "--name", "feature/x", "up", "--dry-run"}, &out, &errb, getenv)
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	for _, want := range []string{"would claim port 4100", "would run: sh -c mix setup", "demo-feature-x would be up at http://localhost:4100"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb.String())
		}
	}
	if _, err := os.Stat(filepath.Join(wt, ".env.worktree")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote .env.worktree")
	}
}

func TestUpErrorExitsOne(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "phoenix")
	var out, errb bytes.Buffer
	code := Main([]string{"--root", root, "--worktree", wt, "--name", "???", "up", "--dry-run", "--skip-setup"}, &out, &errb, getenv)
	if code != 1 || !strings.Contains(errb.String(), "hades: up: label for worktree name \"???\"") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestUpRejectsUnknownFlagsAndArgs(t *testing.T) {
	for _, args := range [][]string{{"up", "--bogus"}, {"up", "extra"}} {
		var out, errb bytes.Buffer
		if code := Main(args, &out, &errb, func(string) string { return "" }); code != 2 || !strings.Contains(errb.String(), "usage: hades up") {
			t.Fatalf("%v: code=%d err=%q", args, code, errb.String())
		}
	}
}

func TestCommandFlagsReachTheCommand(t *testing.T) {
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case "HADES_HOST_CONFIG":
			return "laptop"
		case "HOME":
			return t.TempDir()
		}
		return ""
	}
	// --check is caddy-reload's own flag; the global parser must pass it on.
	code := Main([]string{"caddy-reload", "--check"}, &out, &errb, getenv)
	if code != 1 || !strings.Contains(errb.String(), "caddy-reload: no host config") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	// Globals after the command word, mixed with command flags, still apply.
	errb.Reset()
	if code := Main([]string{"version", "-v", "--json"}, &out, &errb, getenv); code != 0 {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestUpNonWebReportsNoWebPort(t *testing.T) {
	root, wt, getenv := laptopUpFixture(t, "go")
	var out, errb bytes.Buffer
	code := Main([]string{"--root", root, "--worktree", wt, "--name", "feature/x", "up", "--dry-run"}, &out, &errb, getenv)
	if code != 0 || !strings.Contains(errb.String(), "hades: dry run: demo-feature-x would be up (no web port)") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}
