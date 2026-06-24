package gitmeta

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// git runs a git command in dir with a throwaway identity so the test never touches global config.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_AUTHOR_DATE=2020-01-01T00:00:00",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE=2020-01-01T00:00:00",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDiffBaseFallsBackToEmptyTreeOnSingleCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")

	// zero commits: HEAD~1 cannot resolve → empty-tree base, no crash.
	if base := DiffBase(dir); base != EmptyTreeHash {
		t.Errorf("zero-commit DiffBase = %q; want empty-tree hash", base)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "first")

	// single commit: still no parent → must stay on empty-tree base (the bug we are pre-empting).
	if base := DiffBase(dir); base != EmptyTreeHash {
		t.Errorf("single-commit DiffBase = %q; want empty-tree hash", base)
	}
	if files := ChangedFiles(dir); len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("ChangedFiles = %v; want [a.txt] (everything new vs empty tree)", files)
	}

	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("yo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "b.txt")
	git(t, dir, "commit", "-q", "-m", "second")

	// now a parent exists → real HEAD~1.
	if base := DiffBase(dir); base != "HEAD~1" {
		t.Errorf("multi-commit DiffBase = %q; want HEAD~1", base)
	}
	if files := ChangedFiles(dir); len(files) != 1 || files[0] != "b.txt" {
		t.Errorf("ChangedFiles = %v; want [b.txt] (changed since HEAD~1)", files)
	}
}

func TestDiffBaseOnNonGitDir(t *testing.T) {
	if base := DiffBase(t.TempDir()); base != EmptyTreeHash {
		t.Errorf("non-git DiffBase = %q; want empty-tree hash (crash-free fallback)", base)
	}
}

func TestHasParentCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if HasParentCommit(dir) {
		t.Errorf("zero-commit repo should have no parent")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "first")
	if HasParentCommit(dir) {
		t.Errorf("single-commit repo should have no parent (this guards verify against CHANGED-flood)")
	}
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
	if !HasParentCommit(dir) {
		t.Errorf("two-commit repo should have a parent")
	}
}

// ChangedFiles must return paths relative to the passed dir, not the git top-level, so verify can
// run with --repo pointing at a subdirectory without silently never matching.
func TestChangedFilesRelativeToSubdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	top := t.TempDir()
	git(t, top, "init", "-q")
	sub := filepath.Join(top, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "c.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "first")
	if err := os.WriteFile(filepath.Join(sub, "c.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "second")

	files := ChangedFiles(sub) // pointed at the subdir
	if len(files) != 1 || files[0] != "c.txt" {
		t.Errorf("ChangedFiles(subdir) = %v; want [c.txt] (subdir-relative, not pkg/c.txt)", files)
	}
}
