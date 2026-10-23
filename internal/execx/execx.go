// Package execx is the single way hades runs external commands. It provides a
// real runner, a recording fake for tests and a dry-run wrapper.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Cmd describes one command invocation.
type Cmd struct {
	Argv  []string
	Dir   string
	Env   []string // replaces the environment when non-nil, inherited when nil
	Stdin io.Reader
}

// Result is the captured outcome of a command.
type Result struct {
	ExitCode       int
	Stdout, Stderr string
}

// ExitError reports a command that ran and exited non-zero. Callers use
// errors.As to tell it apart from a command that could not be started.
type ExitError struct {
	Cmd    Cmd
	Result Result
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit status %d", strings.Join(e.Cmd.Argv, " "), e.Result.ExitCode)
	if s := strings.TrimSpace(e.Result.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

// Runner runs a command. A non-zero exit is returned as *ExitError.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// Real runs commands with os/exec.
type Real struct {
	Prepend []string  // directories prepended to PATH when they exist on disk
	Log     io.Writer // when set, receives a line per command
}

// ToolPrepend returns the directories where version-managed tools live, in
// lookup order, followed by extra.
func ToolPrepend(home string, extra []string) []string {
	return append([]string{
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".local", "bin"),
	}, extra...)
}

// Run implements Runner.
func (r *Real) Run(ctx context.Context, c Cmd) (Result, error) {
	if len(c.Argv) == 0 {
		return Result{}, errors.New("execx: empty argv")
	}
	env := c.Env
	if env == nil {
		env = os.Environ()
	}
	env, path := r.withPath(env)

	bin, err := lookPath(c.Argv[0], path, c.Dir)
	if err != nil {
		return Result{}, err
	}
	if r.Log != nil {
		fmt.Fprintf(r.Log, "+ %s\n", strings.Join(c.Argv, " "))
	}

	cmd := exec.CommandContext(ctx, bin, c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = env
	cmd.Stdin = c.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr == nil {
		return res, nil
	}
	var xe *exec.ExitError
	if errors.As(runErr, &xe) {
		res.ExitCode = xe.ExitCode()
		return res, &ExitError{Cmd: c, Result: res}
	}
	return res, runErr
}

// withPath returns env with the existing Prepend directories added to PATH,
// and the resulting PATH value.
func (r *Real) withPath(env []string) ([]string, string) {
	idx, path := -1, ""
	for i, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			idx, path = i, v
		}
	}
	if idx < 0 {
		path = os.Getenv("PATH")
	}
	path, changed := prependPath(r.Prepend, path)
	if !changed {
		return env, path
	}
	out := append([]string(nil), env...)
	if idx >= 0 {
		out[idx] = "PATH=" + path
	} else {
		out = append(out, "PATH="+path)
	}
	return out, path
}

// prependPath puts the prepend directories that exist on disk in front of
// path, reporting whether any were added.
func prependPath(prepend []string, path string) (string, bool) {
	var dirs []string
	for _, d := range prepend {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		return path, false
	}
	return strings.Join(append(dirs, path), string(os.PathListSeparator)), true
}

// LookPath resolves name the way Real runs it: the existing prepend
// directories first, then pathEnv.
func LookPath(name string, prepend []string, pathEnv string) (string, error) {
	path, _ := prependPath(prepend, pathEnv)
	return lookPath(name, path, "")
}

// lookPath resolves name against the given PATH value (not the process PATH).
func lookPath(name, path, dir string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if !filepath.IsAbs(name) && dir != "" {
			name = filepath.Join(dir, name)
		}
		return name, nil
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// Fake records every call and answers through Handle.
type Fake struct {
	mu     sync.Mutex
	Calls  []Cmd
	Handle func(Cmd) (Result, error) // default: exit 0, no output
}

// Run implements Runner.
func (f *Fake) Run(_ context.Context, c Cmd) (Result, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, c)
	h := f.Handle
	f.mu.Unlock()
	if h == nil {
		return Result{}, nil
	}
	return h(c)
}

// DryRun prints mutating commands instead of running them and forwards
// read-only ones to Next.
type DryRun struct {
	Out  io.Writer
	Next Runner
}

// readOnly reports whether c is safe to execute during a dry run. Extend this
// as more read-only commands are needed.
func readOnly(c Cmd) bool {
	if len(c.Argv) == 0 || c.Argv[0] != "git" {
		return false
	}
	args := c.Argv[1:]
	for len(args) >= 2 && args[0] == "-C" { // skip "-C <dir>"
		args = args[2:]
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "rev-parse":
		return true
	case "branch":
		// Only listing forms; `git branch <name>` or `-D` would mutate.
		return len(args) == 1 || args[1] == "--show-current" || args[1] == "--list"
	}
	return false
}

// Run implements Runner.
func (d *DryRun) Run(ctx context.Context, c Cmd) (Result, error) {
	if readOnly(c) {
		return d.Next.Run(ctx, c)
	}
	fmt.Fprintf(d.Out, "would run: %s (in %s)\n", strings.Join(c.Argv, " "), c.Dir)
	return Result{ExitCode: 0}, nil
}
