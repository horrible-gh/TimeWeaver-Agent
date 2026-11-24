//go:build windows

package tasks

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRecursiveHousekeepNeverTraversesJunction(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	now := time.Now()
	target := filepath.Join(outside, "old.log")
	housekeepFile(t, target, now.Add(-72*time.Hour))
	junction := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd.exe", "/D", "/C", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	var report housekeepReport
	if err := housekeepRecursive(context.Background(), root, 1, now, &report, true); err != nil {
		t.Fatal(err)
	}
	assertHousekeepExists(t, target, true)
	assertHousekeepExists(t, junction, true)
	if err := HousekeepRecursive(context.Background(), junction, 1, now); err == nil {
		t.Fatal("accepted junction root")
	}
	assertHousekeepExists(t, target, true)
}
