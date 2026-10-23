package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/testutil"
)

type captured struct{ stdout, stderr string }

// runInCapture runs hades in dir (init works on the current directory).
func runInCapture(t *testing.T, dir string, args ...string) (int, captured) {
	t.Helper()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb, func(string) string { return "" })
	return code, captured{out.String(), errb.String()}
}

func runIn(t *testing.T, dir string, args ...string) int {
	t.Helper()
	code, _ := runInCapture(t, dir, args...)
	return code
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInitWritesFilesOnce(t *testing.T) {
	root, _ := testutil.TempGitRepoWithWorktree(t)
	code := runIn(t, root, "init")
	if code != 0 {
		t.Fatal(code)
	}
	for _, f := range []string{"hades.toml", "paseo.json", "orca.yaml"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatal(f)
		}
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if !strings.Contains(string(gi), ".env.worktree") || !strings.Contains(string(gi), ".envrc") {
		t.Fatal(string(gi))
	}
	var pj map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(root, "paseo.json")), &pj); err != nil {
		t.Fatal(err)
	}
	if pj["worktree"].(map[string]any)["servicePorts"].(map[string]any)["portScript"] != "hades-port" {
		t.Fatal(pj)
	}
	if err := os.WriteFile(filepath.Join(root, "orca.yaml"), []byte("scripts:\n  setup: make\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runInCapture(t, root, "init")
	if code != 0 {
		t.Fatal(code, out.stderr)
	}
	if !strings.Contains(out.stderr, "orca.yaml") || !strings.Contains(out.stderr, "hades up") {
		t.Fatal(out.stderr)
	}
	if b := mustRead(t, filepath.Join(root, "orca.yaml")); !strings.Contains(string(b), "make") || strings.Contains(string(b), "hades") {
		t.Fatal("overwrote existing orca.yaml")
	}
	if !strings.Contains(out.stderr, "hades.toml exists") {
		t.Fatal(out.stderr)
	}
	gi2 := mustRead(t, filepath.Join(root, ".gitignore"))
	if strings.Count(string(gi2), ".envrc") != 1 {
		t.Fatalf("gitignore duplicated: %q", gi2)
	}
}

func TestInitPhoenixPrintsConfigSnippet(t *testing.T) {
	root, _ := testutil.TempGitRepoWithWorktree(t)
	src := filepath.Join("..", "preset", "testdata", "phoenix")
	for _, f := range []string{"mix.exs", "mix.lock"} {
		if err := os.WriteFile(filepath.Join(root, f), mustRead(t, filepath.Join(src, f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := runInCapture(t, root, "init")
	if code != 0 {
		t.Fatal(code, out.stderr)
	}
	for _, want := range []string{"config/dev.exs", "config/test.exs", "DB_SUFFIX", "MIX_TEST_PARTITION", "PORT"} {
		if !strings.Contains(out.stderr, want) {
			t.Fatalf("missing %q in %s", want, out.stderr)
		}
	}
}

func TestInitForceOverwritesAndKeepsGitignore(t *testing.T) {
	root, _ := testutil.TempGitRepoWithWorktree(t)
	if err := os.WriteFile(filepath.Join(root, "hades.toml"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runIn(t, root, "init", "--force"); code != 0 {
		t.Fatal(code)
	}
	if b := mustRead(t, filepath.Join(root, "hades.toml")); strings.Contains(string(b), "old") || !strings.Contains(string(b), "project = ") {
		t.Fatalf("hades.toml not rewritten: %s", b)
	}
	if gi := string(mustRead(t, filepath.Join(root, ".gitignore"))); gi != "node_modules\n.env.worktree\n.envrc\n" {
		t.Fatalf("gitignore = %q", gi)
	}
}

func TestInitADEWorktrunkOnly(t *testing.T) {
	root, _ := testutil.TempGitRepoWithWorktree(t)
	code, out := runInCapture(t, root, "init", "--ade", "wt")
	if code != 0 {
		t.Fatal(code, out.stderr)
	}
	if !strings.Contains(out.stderr, `pre-start = "hades up --name {{ branch }}"`) || !strings.Contains(out.stderr, ".config/wt.toml") {
		t.Fatal(out.stderr)
	}
	for _, f := range []string{"paseo.json", "orca.yaml"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Fatalf("%s written without --ade", f)
		}
	}
}

func TestInitUnknownADEIsUsage(t *testing.T) {
	root, _ := testutil.TempGitRepoWithWorktree(t)
	code, out := runInCapture(t, root, "init", "--ade", "vim")
	if code != 2 || !strings.Contains(out.stderr, "usage:") {
		t.Fatal(code, out.stderr)
	}
}

func TestInitOutsideRepo(t *testing.T) {
	code, out := runInCapture(t, t.TempDir(), "init")
	if code != 1 || !strings.Contains(out.stderr, "hades: init: not inside a git repository") {
		t.Fatal(code, out.stderr)
	}
}
