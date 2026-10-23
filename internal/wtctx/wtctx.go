// Package wtctx resolves the root checkout, worktree path and worktree name
// from flags, ADE environment variables or git.
package wtctx

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/quotedo/hades/internal/execx"
)

// Context is the resolved worktree identity.
type Context struct {
	Root, Worktree, Name, ADE string
}

// Flags are explicit overrides; empty means unset.
type Flags struct {
	Root, Worktree, Name, ADE string
}

type family struct {
	ade                  string
	rootVars             []string
	worktreeVar, nameVar string
}

var (
	paseo = family{"paseo", []string{"PASEO_SOURCE_CHECKOUT_PATH", "PASEO_ROOT_PATH"}, "PASEO_WORKTREE_PATH", "PASEO_BRANCH_NAME"}
	orca  = family{"orca", []string{"ORCA_ROOT_PATH"}, "ORCA_WORKTREE_PATH", "ORCA_WORKSPACE_NAME"}
)

// Resolve fills in each value from flags, then Paseo env, then Orca env, then
// git. Git is consulted only for values still missing.
func Resolve(ctx context.Context, f Flags, getenv func(string) string, cwd string, run execx.Runner) (Context, error) {
	var families []family
	switch f.ADE {
	case "paseo":
		families = []family{paseo}
	case "orca":
		families = []family{orca}
	case "wt", "shell":
	default:
		families = []family{paseo, orca}
	}

	c := Context{Root: f.Root, Worktree: f.Worktree, Name: f.Name}
	used := ""
	for _, fam := range families {
		fill := func(cur *string, names ...string) {
			if *cur != "" {
				return
			}
			for _, n := range names {
				if v := getenv(n); v != "" {
					*cur = v
					if used == "" {
						used = fam.ade
					}
					return
				}
			}
		}
		fill(&c.Root, fam.rootVars...)
		fill(&c.Worktree, fam.worktreeVar)
		fill(&c.Name, fam.nameVar)
	}

	switch {
	case f.ADE == "paseo" || f.ADE == "orca" || f.ADE == "wt":
		c.ADE = f.ADE
	case used != "":
		c.ADE = used
	default:
		c.ADE = "shell"
	}

	if c.Worktree == "" {
		out, err := git(ctx, run, cwd, "rev-parse", "--show-toplevel")
		if err != nil {
			return c, err
		}
		c.Worktree = out
	}
	if c.Root == "" {
		out, err := git(ctx, run, cwd, "rev-parse", "--git-common-dir")
		if err != nil {
			return c, err
		}
		if !filepath.IsAbs(out) {
			out = filepath.Join(cwd, out)
		}
		c.Root = filepath.Dir(filepath.Clean(out))
	}
	if c.Name == "" {
		out, err := git(ctx, run, cwd, "branch", "--show-current")
		if err != nil {
			return c, err
		}
		c.Name = out
		if c.Name == "" { // detached HEAD
			c.Name = filepath.Base(c.Worktree)
		}
	}

	c.Root = evalIfExists(c.Root)
	c.Worktree = evalIfExists(c.Worktree)
	return c, nil
}

func git(ctx context.Context, run execx.Runner, cwd string, args ...string) (string, error) {
	res, err := run.Run(ctx, execx.Cmd{Argv: append([]string{"git", "-C", cwd}, args...)})
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

func evalIfExists(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
