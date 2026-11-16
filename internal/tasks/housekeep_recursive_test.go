package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

func housekeepFile(t *testing.T, path string, modified time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep-or-remove"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}
func assertHousekeepExists(t *testing.T, path string, exists bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if exists && err != nil {
		t.Fatalf("expected preserved %s: %v", path, err)
	}
	if !exists && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected removed %s: %v", path, err)
	}
}
func TestRecursiveHousekeepFilesAtAllDepthsAndBoundary(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)
	old, recent, boundary := now.Add(-72*time.Hour), now.Add(-time.Hour), now.Add(-24*time.Hour)
	expired := []string{"old.log", "recent-folder/old.log", "old-folder/deep/old.log"}
	kept := []string{"recent.log", "old-folder/new.log", "old-folder/deep/boundary.log"}
	for _, name := range expired {
		housekeepFile(t, filepath.Join(root, name), old)
	}
	for _, name := range kept {
		housekeepFile(t, filepath.Join(root, name), recent)
	}
	housekeepFile(t, filepath.Join(root, kept[2]), boundary)
	if err := os.Mkdir(filepath.Join(root, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old-folder", "old-folder/deep", "empty"} {
		if err := os.Chtimes(filepath.Join(root, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := HousekeepRecursive(context.Background(), root, 1, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range expired {
		assertHousekeepExists(t, filepath.Join(root, name), false)
	}
	for _, name := range kept {
		assertHousekeepExists(t, filepath.Join(root, name), true)
	}
	for _, name := range []string{"", "recent-folder", "old-folder", "old-folder/deep", "empty"} {
		assertHousekeepExists(t, filepath.Join(root, name), true)
	}
}
func TestExecutorHonorsRecursiveFlagAndDefaultsOff(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[recursive], func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			days := 1
			nested := filepath.Join(root, "nested", "old.log")
			housekeepFile(t, nested, now.Add(-72*time.Hour))
			task := protocol.Task{Type: "housekeep", DestinationPath: &root, HouseKeepDays: &days, HouseKeepRecursive: protocol.Bool(recursive)}
			if code, msg := (Executor{Now: func() time.Time { return now }}).Run(context.Background(), task); code != 0 {
				t.Fatal(code, msg)
			}
			assertHousekeepExists(t, nested, !recursive)
		})
	}
}
func TestRecursiveHousekeepDoesNotFollowLinksOrCycles(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	now := time.Now()
	target := filepath.Join(outside, "old.log")
	housekeepFile(t, target, now.Add(-72*time.Hour))
	for _, link := range []struct{ name, target string }{{"outside", outside}, {"file", target}, {"cycle", root}, {"broken", filepath.Join(outside, "absent")}} {
		if err := os.Symlink(link.target, filepath.Join(root, link.name)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	housekeepFile(t, filepath.Join(root, "nested", "old.log"), now.Add(-72*time.Hour))
	if err := HousekeepRecursive(context.Background(), root, 1, now); err != nil {
		t.Fatal(err)
	}
	assertHousekeepExists(t, target, true)
	for _, name := range []string{"outside", "file", "cycle", "broken"} {
		assertHousekeepExists(t, filepath.Join(root, name), true)
	}
	assertHousekeepExists(t, filepath.Join(root, "nested", "old.log"), false)
	linkedRoot := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(outside, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := HousekeepRecursive(context.Background(), linkedRoot+string(os.PathSeparator), 1, now); err == nil {
		t.Fatal("accepted linked root")
	}
	assertHousekeepExists(t, target, true)
}
func TestRecursiveHousekeepNoopAndInvalidRoot(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	file := filepath.Join(root, "nested", "old")
	housekeepFile(t, file, now.Add(-72*time.Hour))
	for _, days := range []int{0, -1, 1000000000} {
		if err := HousekeepRecursive(context.Background(), root, days, now); err != nil {
			t.Fatal(err)
		}
		assertHousekeepExists(t, file, true)
	}
	if err := HousekeepRecursive(context.Background(), filepath.Join(root, "absent"), 1, now); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := HousekeepRecursive(context.Background(), file, 1, now); err == nil {
		t.Fatal("accepted file as root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := HousekeepRecursive(ctx, root, 1, now); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertHousekeepExists(t, file, true)
}
