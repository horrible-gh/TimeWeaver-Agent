//go:build linux

package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRecursiveHousekeepPreservesSpecialFiles(t *testing.T) {
	root := t.TempDir()
	pipe := filepath.Join(root, "pipe")
	now := time.Now()
	past := now.Add(-72 * time.Hour)
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(pipe, past, past); err != nil {
		t.Fatal(err)
	}
	if err := HousekeepRecursive(context.Background(), root, 1, now); err != nil {
		t.Fatal(err)
	}
	assertHousekeepExists(t, pipe, true)
}
func TestRecursiveHousekeepPermissionErrorFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	child := filepath.Join(root, "blocked")
	now := time.Now()
	housekeepFile(t, filepath.Join(child, "old"), now.Add(-72*time.Hour))
	if err := os.Chmod(child, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(child, 0700)
	if err := HousekeepRecursive(context.Background(), root, 1, now); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("wanted permission error, got %v", err)
	}
}
