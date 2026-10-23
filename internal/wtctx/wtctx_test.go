package wtctx_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/quotedo/hades/internal/execx"
	"github.com/quotedo/hades/internal/testutil"
	"github.com/quotedo/hades/internal/wtctx"
)

func mustEval(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		panic(err)
	}
	return r
}

func TestResolveFlagsBeatEnvBeatGit(t *testing.T) {
	root, wt := testutil.TempGitRepoWithWorktree(t)
	env := map[string]string{"PASEO_SOURCE_CHECKOUT_PATH": "/p/root", "PASEO_WORKTREE_PATH": "/p/wt", "PASEO_BRANCH_NAME": "paseo-branch"}
	getenv := func(k string) string { return env[k] }
	c, err := wtctx.Resolve(context.Background(), wtctx.Flags{Name: "flagname"}, getenv, wt, &execx.Real{})
	if err != nil || c.Name != "flagname" || c.Worktree != "/p/wt" || c.Root != "/p/root" || c.ADE != "paseo" {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = wtctx.Resolve(context.Background(), wtctx.Flags{}, func(string) string { return "" }, wt, &execx.Real{})
	if c.Name != "feature/x" || c.Worktree != mustEval(wt) || c.Root != mustEval(root) || c.ADE != "shell" {
		t.Fatalf("%+v", c)
	}
}

func TestResolveOrcaAndADERestriction(t *testing.T) {
	env := map[string]string{"ORCA_ROOT_PATH": "/o/root", "ORCA_WORKTREE_PATH": "/o/wt", "ORCA_WORKSPACE_NAME": "ws", "PASEO_WORKTREE_PATH": "/p/wt"}
	getenv := func(k string) string { return env[k] }
	c, _ := wtctx.Resolve(context.Background(), wtctx.Flags{ADE: "orca"}, getenv, t.TempDir(), &execx.Fake{})
	if c.Worktree != "/o/wt" || c.Name != "ws" || c.ADE != "orca" {
		t.Fatalf("%+v", c)
	}
}

func TestResolveNoGitWhenEverythingProvided(t *testing.T) {
	fake := &execx.Fake{}
	env := map[string]string{"PASEO_ROOT_PATH": "/p/root", "PASEO_WORKTREE_PATH": "/p/wt", "PASEO_BRANCH_NAME": "b"}
	c, err := wtctx.Resolve(context.Background(), wtctx.Flags{}, func(k string) string { return env[k] }, "/cwd", fake)
	if err != nil || len(fake.Calls) != 0 || c.Root != "/p/root" || c.Name != "b" {
		t.Fatalf("%+v %v %v", c, err, fake.Calls)
	}
}

func TestResolveADEWtIgnoresEnv(t *testing.T) {
	env := map[string]string{"PASEO_WORKTREE_PATH": "/p/wt", "ORCA_WORKTREE_PATH": "/o/wt"}
	fake := &execx.Fake{Handle: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "/g/wt\n"}, nil
	}}
	c, _ := wtctx.Resolve(context.Background(), wtctx.Flags{ADE: "wt"}, func(k string) string { return env[k] }, "/cwd", fake)
	if c.Worktree != "/g/wt" || c.ADE != "wt" {
		t.Fatalf("%+v", c)
	}
}

func TestResolveDetachedHeadFallsBackToBase(t *testing.T) {
	fake := &execx.Fake{} // empty output for every git call
	c, _ := wtctx.Resolve(context.Background(), wtctx.Flags{Worktree: "/x/my-wt", Root: "/x/root"}, func(string) string { return "" }, "/cwd", fake)
	if c.Name != "my-wt" {
		t.Fatalf("%+v", c)
	}
}
