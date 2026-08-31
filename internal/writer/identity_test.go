package writer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The property every authority proof in this package rests on: an identity is a property of the
// object, so it survives closing and reopening the same directory, and it changes when a different
// directory takes the name. Neither half is provable from a pathname, which is why this is asserted
// before anything else uses it.
func TestObjectIdentityIsStableAcrossReopenAndDiffersAfterReplacement(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "root")
	replacement := filepath.Join(parent, "replacement")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacement, 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := identityOfName(dir)
	if err != nil {
		t.Fatalf("read the identity of a directory: %v", err)
	}
	if !first.known() {
		t.Fatal("an existing directory produced an empty identity")
	}
	second, err := identityOfName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("the identity of one directory changed between two reads: %q then %q", first, second)
	}

	// the same object through a handle, which is the form every authority check compares
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	held, err := identityOfRoot(r)
	if err != nil {
		r.Close()
		t.Fatalf("read the identity of an open root: %v", err)
	}
	if held != first {
		t.Fatalf("the handle and the name disagree about the same directory: %q vs %q", held, first)
	}

	// Windows keeps a directory with an open handle from being renamed at all, so the swap is
	// staged with nothing held. What is being proved here is that the IDENTITY moved, not that a
	// handle survived; the handle half is asserted below where the platform allows it.
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, filepath.Join(parent, "parked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, dir); err != nil {
		t.Fatal(err)
	}
	swapped, err := identityOfName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if swapped == first {
		t.Fatalf("a different directory at the same name produced the same identity %q, so identity is being read from the pathname", swapped)
	}
}

// The other half, where the platform allows it: a handle identifies the object it was opened on,
// not the name it was opened through. This is what makes "the name moved under us" detectable at
// all — the two answers stop agreeing.
func TestOpenRootKeepsItsIdentityWhenTheNameIsRenamedAway(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to rename a directory that has an open handle, so this interleaving is unreachable there; the identity half is TestObjectIdentityIsStableAcrossReopenAndDiffersAfterReplacement")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "root")
	replacement := filepath.Join(parent, "replacement")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	held, err := identityOfRoot(r)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(dir, filepath.Join(parent, "parked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, dir); err != nil {
		t.Fatal(err)
	}

	stillHeld, err := identityOfRoot(r)
	if err != nil {
		t.Fatal(err)
	}
	if stillHeld != held {
		t.Fatalf("an open root stopped identifying the directory it was opened on: %q vs %q", stillHeld, held)
	}
	byName, err := identityOfName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if byName == held {
		t.Fatalf("the name resolved to the same identity as the parked directory it no longer names: %q", byName)
	}
}
