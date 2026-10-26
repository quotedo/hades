// Package cli implements the hades command dispatcher.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/version"
	"github.com/quotedo/hades/internal/wtctx"
)

// Globals are the flags accepted by every command.
type Globals struct {
	wtctx.Flags
	JSON bool
}

// Context carries the process environment into a command.
type Context struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Flags          Globals
	Runner         execx.Runner // runs external commands; tests pass an execx.Fake
}

// fail reports why cmd failed and returns exit code 1.
func (c *Context) fail(cmd string, err error) int {
	fmt.Fprintf(c.Stderr, "hades: %s: %v\n", cmd, err)
	return 1
}

// parseArgs parses a command's own flags and reports whether it can go on,
// else the exit code: 0 after printing usage for --help, 2 after explaining a
// bad flag or a leftover argument.
func parseArgs(ctx *Context, fs *flag.FlagSet, usage string, args []string) (code int, ok bool) {
	fs.SetOutput(io.Discard)
	err := fs.Parse(args)
	if err == nil && fs.NArg() > 0 {
		err = fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintln(ctx.Stdout, usage)
		return 0, false
	case err != nil:
		fmt.Fprintf(ctx.Stderr, "%s: %v\n%s\n", fs.Name(), err, usage)
		return 2, false
	}
	return 0, true
}

// Command runs one subcommand with the arguments left after global flag
// parsing and returns the process exit code (0 success, 1 failure, 2 usage).
type Command func(ctx *Context, args []string) int

// commands is the dispatch table; adding a command is one entry.
var commands = map[string]Command{
	"version": func(ctx *Context, args []string) int {
		if code, ok := parseArgs(ctx, flag.NewFlagSet("version", flag.ContinueOnError), "usage: hades version", args); !ok {
			return code
		}
		fmt.Fprintln(ctx.Stdout, version.Version)
		return 0
	},
	"up":     up,
	"down":   down,
	"gc":     gc,
	"ls":     ls,
	"status": status,
	"doctor": doctor,
	"env":    envCmd,
	"port":   port,
	"init":   initCmd,
	"join":   join,
}

// Main parses global flags (before and after the command word), dispatches
// to the command and returns the exit code.
func Main(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	ctx := &Context{Stdout: stdout, Stderr: stderr, Getenv: getenv, Runner: &execx.Real{}}
	g := &ctx.Flags

	fs := flag.NewFlagSet("hades", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&g.Root, "root", "", "main checkout root")
	fs.StringVar(&g.Worktree, "worktree", "", "worktree path")
	fs.StringVar(&g.Name, "name", "", "worktree name")
	fs.StringVar(&g.ADE, "ade", "", "ADE that invoked hades")
	fs.BoolVar(&g.JSON, "json", false, "print the result as JSON on stdout")

	if err := fs.Parse(args); err != nil {
		return usage(ctx, err)
	}
	if fs.NArg() == 0 {
		return usage(ctx, errors.New("no command given"))
	}
	name := fs.Arg(0)
	globals, rest := splitGlobals(fs, fs.Args()[1:])
	if err := fs.Parse(globals); err != nil {
		return usage(ctx, err)
	}
	cmd, ok := commands[name]
	if !ok {
		return usage(ctx, fmt.Errorf("unknown command %q", name))
	}
	return cmd(ctx, rest)
}

// splitGlobals separates the global flags (and their values) found after the
// command word from everything else, which the command parses itself.
func splitGlobals(fs *flag.FlagSet, args []string) (globals, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return globals, append(rest, args[i:]...)
		}
		name, hasValue := strings.CutPrefix(a, "--")
		if !hasValue {
			name, _ = strings.CutPrefix(a, "-")
		}
		name, _, hasValue = strings.Cut(name, "=")
		f := fs.Lookup(name)
		if !strings.HasPrefix(a, "-") || f == nil {
			rest = append(rest, a)
			continue
		}
		globals = append(globals, a)
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); (!ok || !b.IsBoolFlag()) && !hasValue && i+1 < len(args) {
			i++
			globals = append(globals, args[i])
		}
	}
	return globals, rest
}

// usage prints the command line: on stdout with exit 0 for --help, else on
// stderr after err with exit 2.
func usage(ctx *Context, err error) int {
	w, code := ctx.Stdout, 0
	if !errors.Is(err, flag.ErrHelp) {
		w, code = ctx.Stderr, 2
		fmt.Fprintf(w, "hades: %v\n", err)
	}
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "usage: hades [--root DIR] [--worktree DIR] [--name NAME] [--ade ADE] [--json] <%s> [args]\n", strings.Join(names, "|"))
	return code
}
