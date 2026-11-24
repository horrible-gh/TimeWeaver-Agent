//go:build linux

package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
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
	days := 1
	code, message := (Executor{Now: func() time.Time { return now }}).Run(context.Background(), protocol.Task{Type: "housekeep", DestinationPath: &root, HouseKeepDays: &days, HouseKeepRecursive: true, HouseKeepDeleteEmpty: true})
	if code != 0 || message == nil || !strings.HasPrefix(*message, housekeepWarningPrefix) || !strings.Contains(*message, "Skipped 2 path(s)") {
		t.Fatalf("expected persisted warning, got %d %v", code, message)
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
	for _, recursive := range []bool{false, true} {
		days := 1
		code, message := (Executor{}).Run(context.Background(), protocol.Task{Type: "housekeep", DestinationPath: &root, HouseKeepDays: &days, HouseKeepRecursive: protocol.Bool(recursive)})
		if code == 0 || message == nil || strings.HasPrefix(*message, housekeepWarningPrefix) {
			t.Fatal("root failure was swallowed", code, message)
		}
	}
}
