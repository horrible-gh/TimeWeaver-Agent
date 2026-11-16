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
func TestRecursiveHousekeepSkipsPermissionDeniedChildren(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	now := time.Now()
	past := now.Add(-72 * time.Hour)
	blocked := filepath.Join(root, "blocked")
	readonly := filepath.Join(root, "readonly")
	blockedFile := filepath.Join(blocked, "old")
	readonlyFile := filepath.Join(readonly, "old")
	allowedFile := filepath.Join(root, "writable", "old")
	for _, path := range []string{blockedFile, readonlyFile, allowedFile} {
		housekeepFile(t, path, past)
	}
	// One directory cannot be read; the other can be read but not modified.
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0700)
	if err := os.Chmod(readonly, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(readonly, 0700)
	if err := HousekeepRecursive(context.Background(), root, 1, now); err != nil {
		t.Fatalf("permission-denied children aborted cleanup: %v", err)
	}
	if err := os.Chmod(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readonly, 0700); err != nil {
		t.Fatal(err)
	}
	assertHousekeepExists(t, blockedFile, true)
	assertHousekeepExists(t, readonlyFile, true)
	assertHousekeepExists(t, allowedFile, false)
}

func TestRecursiveHousekeepRootPermissionErrorStillFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0700)
	if err := HousekeepRecursive(context.Background(), root, 1, time.Now()); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("wanted root permission error, got %v", err)
	}
}
