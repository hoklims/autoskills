//go:build windows

package writer

import (
	"fmt"
	"os"
	"syscall"
)

// fileIdentity reads the volume serial number and file index through the OPEN handle.
//
// os.SameFile is not an alternative here. On Windows a FileInfo produced by a stat loads its
// identity lazily by REOPENING the recorded path, so comparing two of them can end up comparing a
// replacement directory with itself — the substitution this identity exists to detect.
func fileIdentity(f *os.File) (FileID, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return "", fmt.Errorf("writer: cannot read the identity of %s: %w", clip(f.Name()), err)
	}
	index := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return FileID(fmt.Sprintf("windows:%d:%d", info.VolumeSerialNumber, index)), nil
}
