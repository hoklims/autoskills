//go:build unix

package writer

import (
	"fmt"
	"os"
	"syscall"
)

// fileIdentity reads device and inode from the OPEN descriptor. Going through the descriptor and
// not through the name is the whole point: fstat answers about the object this process is holding,
// which is exactly what a rename-and-replace cannot change under it.
func fileIdentity(f *os.File) (FileID, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("writer: this build exposes no stable identity for %s, so a captured authorized root cannot be proved", clip(f.Name()))
	}
	return FileID(fmt.Sprintf("unix:%d:%d", uint64(st.Dev), uint64(st.Ino))), nil
}
