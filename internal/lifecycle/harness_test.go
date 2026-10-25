package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quotedo/hades/internal/caddy"
	"github.com/quotedo/hades/internal/config"
	"github.com/quotedo/hades/internal/envfile"
	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/naming"
	"github.com/quotedo/hades/internal/registry"
	"github.com/quotedo/hades/internal/testutil"
	"github.com/quotedo/hades/internal/wtctx"
)

// fakePG records dropped databases; List returns names.
type fakePG struct {
	names    []string
	dropped  []string
	failDrop bool
}

func (f *fakePG) List(context.Context) ([]string, error) { return f.names, nil }

func (f *fakePG) Drop(_ context.Context, name string) error {
	if f.failDrop {
		return errors.New("drop failed")
	}
	f.dropped = append(f.dropped, name)
	return nil
}

// fakeRabbit records vhost calls.
type fakeRabbit struct {
	ensured, deleted []string
}

func (f *fakeRabbit) Ensure(_ context.Context, v string) error {
	f.ensured = append(f.ensured, v)
	return nil
}

func (f *fakeRabbit) Delete(_ context.Context, v string) error {
	f.deleted = append(f.deleted, v)
	return nil
}

// newDeps builds Deps for a host with domain example.test around a fresh git
// worktree holding the preset fixture. The root checkout carries hades.toml
// with project = "demo", so the label is demo-feature-x whatever later tests
// write into the worktree's own hades.toml.
func newDeps(t *testing.T, fixture string) (Deps, wtctx.Context) {
	t.Helper()
	root, wt := testutil.TempGitRepoWithWorktree(t)
	root, wt = resolved(t, root), resolved(t, wt)
	testutil.CopyDir(t, filepath.Join("..", "preset", "testdata", fixture), wt)
	writeFile(t, filepath.Join(root, "hades.toml"), "project = \"demo\"\n")
	writeFile(t, filepath.Join(wt, ".mcp.json"),
		`{"mcpServers": {"tidewave": {"type": "http", "url": "http://localhost:4000/tidewave/mcp"}}}`+"\n")

	state := t.TempDir()
	host := &config.Host{
		Domain:        "example.test",
		LabelTemplate: naming.DefaultLabelTemplate,
		Ports:         config.PortRange{Lo: 4100, Hi: 4119},
		PortsDir:      filepath.Join(state, "ports"),
		SitesDir:      filepath.Join(state, "sites"),
		ManifestsDir:  filepath.Join(state, "manifests"),
		Postgres:      config.Postgres{Host: "127.0.0.1", Port: 5433, User: "postgres", Password: "postgres"},
	}
	reg := registry.New(host)
	if err := reg.EnsureDirs(0o700); err != nil {
		t.Fatal(err)
	}
	d := Deps{
		Host:     host,
		Reg:      reg,
		Run:      &execx.Fake{},
		PG:       &fakePG{},
		Rabbit:   nil,
		User:     UserInfo{Name: "alice", UID: os.Getuid(), Home: t.TempDir()},
		Version:  "test",
		Now:      time.Now,
		Log:      io.Discard,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
	}
	return d, wtctx.Context{Root: root, Worktree: wt, Name: "feature/x", ADE: "shell"}
}

// secondWorktree returns the Context of a second worktree, feature/y of
// another demo checkout, for the same Deps.
func secondWorktree(t *testing.T, fixture string) wtctx.Context {
	t.Helper()
	root, wt := testutil.TempGitRepoWithWorktree(t)
	root, wt = resolved(t, root), resolved(t, wt)
	testutil.CopyDir(t, filepath.Join("..", "preset", "testdata", fixture), wt)
	writeFile(t, filepath.Join(root, "hades.toml"), "project = \"demo\"\n")
	return wtctx.Context{Root: root, Worktree: wt, Name: "feature/y", ADE: "shell"}
}

// newLaptopDeps is newDeps without a domain.
func newLaptopDeps(t *testing.T, fixture string) (Deps, wtctx.Context) {
	t.Helper()
	d, c := newDeps(t, fixture)
	d.Host.Domain = ""
	return d, c
}

// reloadCall is the Caddy reload as argvs prints it.
var reloadCall = strings.Join(caddy.ReloadArgv, " ")

// sitePath is the site file of label in d's sites directory.
func sitePath(t *testing.T, d Deps, label string) string {
	t.Helper()
	p, err := caddy.SitePath(d.Reg.SitesDir, label)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// failWhen returns a Fake handler failing every command whose argv[0] is argv0.
func failWhen(argv0 string, code int) func(execx.Cmd) (execx.Result, error) {
	return func(c execx.Cmd) (execx.Result, error) {
		if len(c.Argv) > 0 && c.Argv[0] == argv0 {
			res := execx.Result{ExitCode: code, Stderr: argv0 + " failed"}
			return res, &execx.ExitError{Cmd: c, Result: res}
		}
		return execx.Result{}, nil
	}
}

func argvs(f *execx.Fake) []string {
	out := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		out[i] = strings.Join(c.Argv, " ")
	}
	return out
}

func lastCallWithPrefix(f *execx.Fake, prefix string) execx.Cmd {
	for i := len(f.Calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.Join(f.Calls[i].Argv, " "), prefix) {
			return f.Calls[i]
		}
	}
	return execx.Cmd{}
}

func toMap(kv []envfile.KV) map[string]string {
	m := map[string]string{}
	for _, e := range kv {
		m[e.Key] = e.Value
	}
	return m
}
