//go:build !unix && !windows

package writer

import (
	"fmt"
	"os"
	"runtime"
)

// fileIdentity refuses instead of degrading to a name comparison. A platform that exposes no
// stable object identity cannot prove that the directory a mutation is about to write through is
// the one it captured, and answering "the paths match" would be the exact substitution this
// package refuses everywhere else. No identity, no mutation.
func fileIdentity(f *os.File) (FileID, error) {
	return "", fmt.Errorf("writer: %s exposes no stable filesystem object identity, so the authorized root of %s cannot be proved and nothing is written",
		runtime.GOOS, clip(f.Name()))
}
