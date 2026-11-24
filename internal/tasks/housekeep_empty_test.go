package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

func TestHousekeepDeleteEmptyDirectories(t *testing.T) {
	for _, tc := range []struct {
		name      string
		recursive bool
		enabled   bool
		days      int
	}{
		{"off", true, false, 1},
		{"direct", false, true, 1},
		{"recursive", true, true, 1},
		{"directories-only", true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			for _, dir := range []string{"empty", "nested/empty"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			old := filepath.Join(root, "expired", "old.log")
			recent := filepath.Join(root, "recent", "new.log")
			housekeepFile(t, old, now.Add(-72*time.Hour))
			housekeepFile(t, recent, now)
			task := protocol.Task{Type: "housekeep", DestinationPath: &root, HouseKeepDays: &tc.days, HouseKeepRecursive: protocol.Bool(tc.recursive), HouseKeepDeleteEmpty: protocol.Bool(tc.enabled)}
			if code, msg := (Executor{Now: func() time.Time { return now }}).Run(context.Background(), task); code != 0 {
				t.Fatal(code, msg)
			}
			assertHousekeepExists(t, root, true)
			assertHousekeepExists(t, filepath.Join(root, "empty"), !tc.enabled)
			assertHousekeepExists(t, filepath.Join(root, "nested"), !(tc.enabled && tc.recursive))
			assertHousekeepExists(t, filepath.Join(root, "expired"), !(tc.enabled && tc.recursive && tc.days > 0))
			assertHousekeepExists(t, old, !(tc.recursive && tc.days > 0))
			assertHousekeepExists(t, recent, true)
		})
	}
}

func TestEmptyDirectoryRemovalPreservesLinksAndRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	now := time.Now()
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	days := 0
	task := protocol.Task{Type: "housekeep", DestinationPath: &root, HouseKeepDays: &days, HouseKeepRecursive: true, HouseKeepDeleteEmpty: true}
	if code, msg := (Executor{Now: func() time.Time { return now }}).Run(context.Background(), task); code != 0 {
		t.Fatal(code, msg)
	}
	for _, path := range []string{root, outside, link} {
		assertHousekeepExists(t, path, true)
	}
	task.DestinationPath = &link
	if code, _ := (Executor{}).Run(context.Background(), task); code == 0 {
		t.Fatal("linked root accepted")
	}
}
