// Package cli implements the hades command dispatcher.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/quotedo/hades/internal/version"
)

// Globals are the flags accepted by every command.
type Globals struct {
	Root, Worktree, Name, ADE string
	JSON, Verbose             bool
}

// Context carries the process environment into a command.
type Context struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Flags          Globals
}

// Command runs one subcommand with the arguments left after global flag
// parsing and returns the process exit code (0 success, 1 failure, 2 usage).
type Command func(ctx *Context, args []string) int

// commands is the dispatch table; adding a command is one entry.
var commands = map[string]Command{
	"version": func(ctx *Context, _ []string) int {
		fmt.Fprintln(ctx.Stdout, version.Version)
		return 0
	},
	"caddy-reload": caddyReload,
	"up":           up,
	"down":         down,
	"gc":           gc,
	// Placeholder until the real port command replaces it.
	"port": func(ctx *Context, _ []string) int {
		fmt.Fprintln(ctx.Stderr, "port: not implemented")
		return 1
	},
}

// Main parses global flags (before and after the command word), dispatches
// to the command and returns the exit code.
func Main(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	ctx := &Context{Stdout: stdout, Stderr: stderr, Getenv: getenv}
	g := &ctx.Flags

	fs := flag.NewFlagSet("hades", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&g.Root, "root", "", "main checkout root")
	fs.StringVar(&g.Worktree, "worktree", "", "worktree path")
	fs.StringVar(&g.Name, "name", "", "worktree name")
	fs.StringVar(&g.ADE, "ade", "", "ADE that invoked hades")
	fs.BoolVar(&g.JSON, "json", false, "print the result as JSON on stdout")
	fs.BoolVar(&g.Verbose, "v", false, "verbose output")

	if err := fs.Parse(args); err != nil {
		return usage(stderr, err)
	}
	if fs.NArg() == 0 {
		return usage(stderr, errors.New("no command given"))
	}
	name := fs.Arg(0)
	globals, rest := splitGlobals(fs, fs.Args()[1:])
	if err := fs.Parse(globals); err != nil {
		return usage(stderr, err)
	}
	cmd, ok := commands[name]
	if !ok {
		return usage(stderr, fmt.Errorf("unknown command %q", name))
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

func usage(w io.Writer, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		err = nil
	}
	if err != nil {
		fmt.Fprintf(w, "hades: %v\n", err)
	}
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "usage: hades [--root DIR] [--worktree DIR] [--name NAME] [--ade ADE] [--json] [-v] <%s> [args]\n", strings.Join(names, "|"))
	if err == nil {
		return 0
	}
	return 2
}
