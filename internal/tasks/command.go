package tasks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

type boundedBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	const max = 32768
	if len(p) >= max {
		b.b = append(b.b[:0], p[len(p)-max:]...)
	} else {
		b.b = append(b.b, p...)
		if len(b.b) > max {
			b.b = append([]byte{}, b.b[len(b.b)-max:]...)
		}
	}
	return n, nil
}
func (b *boundedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.b) }
func command(ctx context.Context, s string) error {
	cmd := shell(ctx, s)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	var stderr boundedBuffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if msg := stderr.String(); msg != "" {
			return errors.New(msg)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return errors.New("command exited unsuccessfully")
		}
		return errors.New("cannot start command")
	}
	return nil
}
