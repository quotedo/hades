package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/quotedo/hades/internal/envfile"
)

// envCmd prints the worktree's .env.worktree for eval or machines.
func envCmd(ctx *Context, args []string) int {
	if !parseArgs(ctx, flag.NewFlagSet("env", flag.ContinueOnError), "usage: hades env [--json]", args) {
		return 2
	}
	wt, err := resolveWorktree(context.Background(), ctx)
	if err != nil {
		return ctx.fail("env", err)
	}
	path := filepath.Join(wt.Worktree, ".env.worktree")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ctx.fail("env", fmt.Errorf("no .env.worktree in %s; run hades up", wt.Worktree))
	} else if err != nil {
		return ctx.fail("env", err)
	}
	kv, err := envfile.ReadEnvWorktree(path)
	if err != nil {
		return ctx.fail("env", err)
	}
	if ctx.Flags.JSON {
		vars := make(map[string]string, len(kv))
		for _, e := range kv {
			vars[e.Key] = e.Value
		}
		if err := writeJSON(ctx.Stdout, vars); err != nil {
			return ctx.fail("env", err)
		}
		return 0
	}
	fmt.Fprint(ctx.Stdout, envfile.Exports(kv))
	return 0
}
