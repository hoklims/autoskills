//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package llm

import "os/exec"

func isolateCommandProcess(command *exec.Cmd) {}
