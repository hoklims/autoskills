// Package gitmeta is the git-provenance foundation for skill verification. Its one job today is
// to compute a safe diff base: a naive `HEAD~1` blows up with `fatal: ambiguous argument 'HEAD~1'`
// on a single-commit repo (no parent), so any "what changed under this skill" comparison must fall
// back to git's well-known empty-tree object. Baking that in now means the upcoming provenance
// feature (which commit introduced this convention, has the referenced path changed since) can
// never regress into that first-commit crash.
package gitmeta

import (
	"os/exec"
	"strings"
)

// EmptyTreeHash is git's canonical empty-tree object id. Diffing against it yields "everything is
// new", which is exactly the correct semantics for the first commit in a repo (no parent exists).
const EmptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// DiffBase returns the revision to diff HEAD against for repoRoot: "HEAD~1" when a parent commit
// exists, otherwise the empty-tree hash. It never errors — an unreadable or non-git directory
// also yields the empty-tree base (treat all current content as new), so callers stay crash-free.
func DiffBase(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", "HEAD~1")
	if err := cmd.Run(); err == nil {
		return "HEAD~1"
	}
	return EmptyTreeHash
}

// HasParentCommit reports whether HEAD has a parent (i.e. the repo has at least two commits).
// Callers wanting "what changed since the previous commit" semantics should gate on this: with no
// parent there is no prior state, so nothing has drifted — diffing against the empty tree instead
// would spuriously report every tracked file as changed.
func HasParentCommit(repoRoot string) bool {
	return DiffBase(repoRoot) != EmptyTreeHash
}

// ChangedFiles returns paths changed between DiffBase(repoRoot) and HEAD, relative to repoRoot
// (via --relative, so passing a subdirectory yields subdir-relative paths and filters to that
// subtree). On any git error it returns nil — provenance is best-effort and must never break a
// verify run. On a single-commit repo this lists all tracked files (everything-is-new vs the
// empty tree); callers that want "changed since parent" should guard with HasParentCommit first.
func ChangedFiles(repoRoot string) []string {
	base := DiffBase(repoRoot)
	out, err := exec.Command("git", "-C", repoRoot, "diff", "--name-only", "--relative", base, "HEAD").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files
}
