package llm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func isolateCommandProcess(command *exec.Cmd) {
	directCancel := command.Cancel
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		systemRoot := os.Getenv("SystemRoot")
		if filepath.IsAbs(systemRoot) {
			taskkill := filepath.Join(systemRoot, "System32", "taskkill.exe")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err := exec.CommandContext(ctx, taskkill, "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run()
			cancel()
			if err == nil {
				return nil
			}
		}
		if directCancel != nil {
			return directCancel()
		}
		return command.Process.Kill()
	}
}
