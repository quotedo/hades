package execx_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/testutil"
)

func TestRealRunnerCapturesExitAndOutput(t *testing.T) {
	r := &execx.Real{}
	res, err := r.Run(context.Background(), execx.Cmd{Argv: []string{"sh", "-c", "echo out; echo err >&2; exit 3"}})
	var ee *execx.ExitError
	if !errors.As(err, &ee) || res.ExitCode != 3 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRealRunnerPrependsExistingPaths(t *testing.T) {
	bindir := t.TempDir()
	testutil.StubTool(t, bindir, "whereami", "#!/bin/bash\necho $PATH")
	r := &execx.Real{Prepend: []string{bindir, "/nonexistent/dir"}}
	res, _ := r.Run(context.Background(), execx.Cmd{Argv: []string{"whereami"}})
	if !strings.HasPrefix(res.Stdout, bindir+":") || strings.Contains(res.Stdout, "/nonexistent") {
		t.Fatal(res.Stdout)
	}
}

func TestRealRunnerDirEnv(t *testing.T) {
	dir := t.TempDir()
	r := &execx.Real{}
	res, err := r.Run(context.Background(), execx.Cmd{
		Argv: []string{"sh", "-c", "pwd; echo $FOO"},
		Dir:  dir,
		Env:  []string{"FOO=bar", "PATH=/usr/bin:/bin"},
	})
	if err != nil || !strings.HasSuffix(res.Stdout, "\nbar\n") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRealRunnerStartFailureIsNotExitError(t *testing.T) {
	r := &execx.Real{}
	_, err := r.Run(context.Background(), execx.Cmd{Argv: []string{"hades-no-such-binary"}})
	var ee *execx.ExitError
	if err == nil || errors.As(err, &ee) {
		t.Fatalf("%v", err)
	}
}

func TestFakeRecordsAndHandles(t *testing.T) {
	f := &execx.Fake{}
	if _, err := f.Run(context.Background(), execx.Cmd{Argv: []string{"x"}}); err != nil || len(f.Calls) != 1 {
		t.Fatal(err, f.Calls)
	}
	f.Handle = func(execx.Cmd) (execx.Result, error) { return execx.Result{Stdout: "s"}, nil }
	if res, _ := f.Run(context.Background(), execx.Cmd{Argv: []string{"y"}}); res.Stdout != "s" || len(f.Calls) != 2 {
		t.Fatal(res, f.Calls)
	}
}

func TestDryRunPrintsInsteadOfRunning(t *testing.T) {
	var out bytes.Buffer
	d := &execx.DryRun{Out: &out}
	res, err := d.Run(context.Background(), execx.Cmd{Argv: []string{"git", "worktree", "add", "x"}, Dir: "/d"})
	if err != nil || res.ExitCode != 0 || out.String() != "would run: git worktree add x (in /d)\n" {
		t.Fatalf("%q %v", out.String(), err)
	}
}

func TestToolPrependIncludesMiseShims(t *testing.T) {
	got := execx.ToolPrepend("/h")
	want := []string{"/h/.asdf/shims", "/h/.local/share/mise/shims", "/usr/local/share/mise/shims", "/h/.local/bin"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
