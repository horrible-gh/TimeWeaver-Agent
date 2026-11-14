//go:build !windows

package tasks

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func shell(ctx context.Context, s string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", s)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd
}
