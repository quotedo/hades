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

func TestRealRunnerDirEnvStdin(t *testing.T) {
	dir := t.TempDir()
	r := &execx.Real{}
	res, err := r.Run(context.Background(), execx.Cmd{
		Argv:  []string{"sh", "-c", "pwd; echo $FOO; cat"},
		Dir:   dir,
		Env:   []string{"FOO=bar", "PATH=/usr/bin:/bin"},
		Stdin: strings.NewReader("in"),
	})
	if err != nil || !strings.HasSuffix(res.Stdout, "\nbar\nin") {
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

func TestDryRunPrintsMutatingAndPassesReadOnlyGit(t *testing.T) {
	var out bytes.Buffer
	next := &execx.Fake{}
	d := &execx.DryRun{Out: &out, Next: next}
	res, err := d.Run(context.Background(), execx.Cmd{Argv: []string{"git", "worktree", "add", "x"}, Dir: "/d"})
	if err != nil || res.ExitCode != 0 || len(next.Calls) != 0 || out.String() != "would run: git worktree add x (in /d)\n" {
		t.Fatalf("%q %v %v", out.String(), err, next.Calls)
	}
	if _, err := d.Run(context.Background(), execx.Cmd{Argv: []string{"git", "rev-parse", "HEAD"}}); err != nil || len(next.Calls) != 1 {
		t.Fatal(err, next.Calls)
	}
	if _, err := d.Run(context.Background(), execx.Cmd{Argv: []string{"git", "-C", "/d", "rev-parse", "--show-toplevel"}}); err != nil || len(next.Calls) != 2 {
		t.Fatal(err, next.Calls)
	}
}

func TestDryRunGitBranchOnlyListingForms(t *testing.T) {
	cases := []struct {
		argv    []string
		forward bool
	}{
		{[]string{"git", "-C", "d", "branch", "-D", "x"}, false},
		{[]string{"git", "branch", "newname"}, false},
		{[]string{"git", "-C", "d", "branch", "--show-current"}, true},
		{[]string{"git", "branch", "--list", "feat*"}, true},
		{[]string{"git", "branch"}, true},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		next := &execx.Fake{}
		d := &execx.DryRun{Out: &out, Next: next}
		if _, err := d.Run(context.Background(), execx.Cmd{Argv: tc.argv}); err != nil {
			t.Fatal(err)
		}
		if got := len(next.Calls) == 1; got != tc.forward || (!tc.forward && !strings.HasPrefix(out.String(), "would run: ")) {
			t.Errorf("%v: forwarded=%v out=%q", tc.argv, got, out.String())
		}
	}
}

func TestToolPrependIncludesMiseShims(t *testing.T) {
	got := execx.ToolPrepend("/h", []string{"/x"})
	want := []string{"/h/.asdf/shims", "/h/.local/share/mise/shims", "/usr/local/share/mise/shims", "/h/.local/bin", "/x"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
