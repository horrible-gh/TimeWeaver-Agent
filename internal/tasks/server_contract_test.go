package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

// The paired CI exports this document from the real server task write/snapshot
// service. Test execution against a disposable local directory, never /cleanup.
func TestServerSnapshotRecursiveHousekeepContract(t *testing.T) {
	path := os.Getenv("TIMEWEAVER_CONTRACT_SNAPSHOT")
	if path == "" {
		t.Skip("paired server snapshot not supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Envelope protocol.Envelope `json:"envelope"`
		ETag     string            `json:"etag"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Device protocol.Device `json:"device"`
	}
	if err = json.Unmarshal(wire.Envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.DecodeSnapshot(wire.Envelope, wire.ETag, data.Device.Identity)
	if err != nil {
		t.Fatal(err)
	}
	tested := 0
	for _, schedule := range snapshot.Schedules {
		for _, detail := range schedule.Details {
			task := detail.Task
			if task.Type != "housekeep" || !bool(task.HouseKeepRecursive) {
				continue
			}
			root := t.TempDir()
			now := time.Now()
			task.DestinationPath = &root
			old := filepath.Join(root, "nested", "deep", "old.log")
			recent := filepath.Join(root, "nested", "new.log")
			housekeepFile(t, old, now.Add(-72*time.Hour))
			housekeepFile(t, recent, now)
			if code, msg := (Executor{Now: func() time.Time { return now }}).Run(context.Background(), task); code != 0 {
				t.Fatal(code, msg)
			}
			assertHousekeepExists(t, old, false)
			assertHousekeepExists(t, recent, true)
			assertHousekeepExists(t, filepath.Dir(old), true)
			tested++
		}
	}
	if tested == 0 {
		t.Fatal("server did not export a recursive housekeep task")
	}
}
