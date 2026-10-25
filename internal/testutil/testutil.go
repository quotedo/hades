// Package testutil holds helpers shared by hades' tests.
package testutil

import (
	"io/fs"
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

// CopyDir copies the regular files under src into dst, directories included.
func CopyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// StubTool writes an executable bash script named name into bindir.
func StubTool(t *testing.T, bindir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bindir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
