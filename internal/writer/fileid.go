package writer

import (
	"fmt"
	"os"
)

// FileID is the identity of a filesystem OBJECT, never of a name.
//
// A canonical pathname proves nothing about which directory it names: renaming a directory away
// and putting a DIFFERENT real directory in its place leaves every path-based comparison agreeing
// with itself, because both sides re-resolve to the same string. Only an identity read out of the
// object — device+inode on Unix, volume serial plus file index on Windows — can contradict the
// substitution.
//
// It is persisted in the manifest, so its string form is a stored format. The platform tag is part
// of it: two operating systems number objects in unrelated spaces, and a manifest captured under
// one must not accidentally match an identity computed under the other.
type FileID string

func (id FileID) known() bool { return id != "" }

// identityOfName reads the identity of the directory a NAME currently resolves to. It follows
// links deliberately: what a name check has to answer is "which object does this name lead to
// now", and the answer is the object at the end of it.
func identityOfName(path string) (FileID, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return directoryIdentity(f, path)
}

// identityOfRoot reads the identity of the directory a HANDLE holds. This is the half a name
// lookup cannot supply: an open root keeps referencing the directory it was opened on even after
// that directory has been renamed away and replaced, so comparing the two is what separates "the
// name still points at my directory" from "I am still holding my directory".
func identityOfRoot(r *os.Root) (FileID, error) {
	f, err := r.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	return directoryIdentity(f, r.Name())
}

func directoryIdentity(f *os.File, name string) (FileID, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("writer: %s is not a directory, so it cannot be an authorized root", clip(name))
	}
	return fileIdentity(f)
}
