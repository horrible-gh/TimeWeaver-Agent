//go:build windows

package tasks

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func shell(ctx context.Context, s string) *exec.Cmd {
	exe := os.Getenv("ComSpec")
	if exe == "" {
		exe = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, exe, "/D", "/S", "/C", s)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		killctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(killctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}
