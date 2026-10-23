package mcp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/mcp"
)

const newURL = "http://localhost:4100/tidewave/mcp"

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileEquals(t *testing.T, got, wantFixture string) {
	t.Helper()
	g, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	w, err := os.ReadFile(wantFixture)
	if err != nil {
		t.Fatal(err)
	}
	if string(g) != string(w) {
		t.Fatalf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", got, g, w)
	}
}

func TestRewireEditsExistingFilesOnly(t *testing.T) {
	wt := t.TempDir()
	copyFixture(t, "testdata/mcp.json", filepath.Join(wt, ".mcp.json"))
	copyFixture(t, "testdata/codex-config.toml", filepath.Join(wt, ".codex/config.toml"))
	f := &execx.Fake{}
	edited, err := mcp.Rewire(context.Background(), wt, mcp.Targets{TidewaveURL: newURL, ChromePath: "/opt/chrome/chrome"}, f)
	if err != nil || len(edited) != 2 {
		t.Fatal(edited, err)
	}
	if edited[0] != ".mcp.json" || edited[1] != ".codex/config.toml" {
		t.Fatal(edited)
	}
	assertFileEquals(t, filepath.Join(wt, ".mcp.json"), "testdata/mcp.after.json")
	assertFileEquals(t, filepath.Join(wt, ".codex/config.toml"), "testdata/codex-config.after.toml")
	if len(f.Calls) != 2 || f.Calls[0].Argv[3] != "update-index" || f.Calls[0].Argv[4] != "--skip-worktree" {
		t.Fatalf("%+v", f.Calls)
	}
}

func TestRewireGrokArrayAndGeminiCursor(t *testing.T) {
	wt := t.TempDir()
	files := map[string]string{
		".gemini/settings.json": "gemini-settings",
		".cursor/mcp.json":      "cursor-mcp",
		".grok/settings.json":   "grok-settings",
	}
	for rel, fx := range files {
		copyFixture(t, "testdata/"+fx+".json", filepath.Join(wt, rel))
	}
	f := &execx.Fake{}
	edited, err := mcp.Rewire(context.Background(), wt, mcp.Targets{TidewaveURL: newURL, ChromePath: "/opt/chrome/chrome"}, f)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gemini/settings.json", ".cursor/mcp.json", ".grok/settings.json"}
	if len(edited) != 3 {
		t.Fatal(edited)
	}
	for i := range want {
		if edited[i] != want[i] {
			t.Fatal(edited)
		}
	}
	for rel, fx := range files {
		assertFileEquals(t, filepath.Join(wt, rel), "testdata/"+fx+".after.json")
	}
	if len(f.Calls) != 3 {
		t.Fatalf("%+v", f.Calls)
	}
}

func TestRewireCodexAppendsAndInsertsURL(t *testing.T) {
	cases := map[string]string{
		"[other]\nk = 1\n":                     "[other]\nk = 1\n\n[mcp_servers.tidewave]\nurl = \"" + newURL + "\"\n",
		"[mcp_servers.tidewave]\nx = 1\n[o]\n": "[mcp_servers.tidewave]\nurl = \"" + newURL + "\"\nx = 1\n[o]\n",
	}
	for in, want := range cases {
		wt := t.TempDir()
		p := filepath.Join(wt, ".codex/config.toml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := mcp.Rewire(context.Background(), wt, mcp.Targets{TidewaveURL: newURL}, &execx.Fake{}); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}

func TestRewireGitFailureIsLoggedAndIgnored(t *testing.T) {
	wt := t.TempDir()
	copyFixture(t, "testdata/cursor-mcp.json", filepath.Join(wt, ".cursor/mcp.json"))
	f := &execx.Fake{Handle: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{ExitCode: 128}, &execx.ExitError{}
	}}
	var log strings.Builder
	edited, err := mcp.Rewire(context.Background(), wt, mcp.Targets{TidewaveURL: newURL, Log: &log}, f)
	if err != nil || len(edited) != 1 {
		t.Fatal(edited, err)
	}
	if log.Len() == 0 {
		t.Fatal("expected a log line")
	}
}

func TestRewireWrongMcpServersTypeIsError(t *testing.T) {
	wt := t.TempDir()
	p := filepath.Join(wt, ".cursor/mcp.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"mcpServers":[]}`), 0o644)
	if _, err := mcp.Rewire(context.Background(), wt, mcp.Targets{TidewaveURL: newURL}, &execx.Fake{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRewireNoTargetsIsNoop(t *testing.T) {
	wt := t.TempDir()
	copyFixture(t, "testdata/mcp.json", filepath.Join(wt, ".mcp.json"))
	f := &execx.Fake{}
	edited, _ := mcp.Rewire(context.Background(), wt, mcp.Targets{}, f)
	if len(edited) != 0 || len(f.Calls) != 0 {
		t.Fatal(edited, f.Calls)
	}
}
