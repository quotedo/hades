// Package testutil holds helpers shared by hades' tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TempGitRepoWithWorktree creates a repository with one commit on main and a
// linked worktree on branch feature/x, returning both paths.
func TempGitRepoWithWorktree(t *testing.T) (root, worktree string) {
	t.Helper()
	return TempGitRepo(t, "root", "feature/x")
}

// TempGitRepo creates a repository in <tmp>/<rootName> with one commit on main
// and a linked worktree <tmp>/wt on the given branch, returning both paths.
func TempGitRepo(t *testing.T, rootName, branch string) (root, worktree string) {
	t.Helper()
	tmp := t.TempDir()
	root = filepath.Join(tmp, rootName)
	worktree = filepath.Join(tmp, "wt")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-b", "main")
	git(t, root, "-c", "user.name=hades-test", "-c", "user.email=hades@test",
		"commit", "--allow-empty", "-m", "init")
	git(t, root, "worktree", "add", worktree, "-b", branch)
	return root, worktree
}

// StubTool writes an executable bash script named name into bindir.
func StubTool(t *testing.T, bindir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bindir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// StubPATH returns a getenv whose PATH is bindir followed by the system dirs.
func StubPATH(t *testing.T, bindir string) func(string) string {
	t.Helper()
	return func(k string) string {
		if k == "PATH" {
			return bindir + ":/usr/bin:/bin"
		}
		return ""
	}
}
