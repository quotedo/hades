package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/quotedo/hades/internal/envfile"
)

const envUsage = "usage: hades env [--json]"

// envCmd prints the worktree's .env.worktree for eval or machines.
func envCmd(ctx *Context, args []string) int {
	flags := flag.NewFlagSet("env", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(ctx.Stderr, "env: %v\n%s\n", err, envUsage)
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(ctx.Stderr, "env: unexpected arguments %q\n%s\n", flags.Args(), envUsage)
		return 2
	}
	wt, err := resolveWorktree(context.Background(), ctx)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: env: %v\n", err)
		return 1
	}
	path := filepath.Join(wt.Worktree, ".env.worktree")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(ctx.Stderr, "hades: env: no .env.worktree in %s; run hades up\n", wt.Worktree)
		return 1
	} else if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: env: %v\n", err)
		return 1
	}
	kv, err := envfile.ReadEnvWorktree(path)
	if err != nil {
		fmt.Fprintf(ctx.Stderr, "hades: env: %v\n", err)
		return 1
	}
	if ctx.Flags.JSON {
		vars := make(map[string]string, len(kv))
		for _, e := range kv {
			vars[e.Key] = e.Value
		}
		if err := writeJSON(ctx.Stdout, vars); err != nil {
			fmt.Fprintf(ctx.Stderr, "hades: env: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprint(ctx.Stdout, envfile.Exports(kv))
	return 0
}
