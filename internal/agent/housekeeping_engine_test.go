package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/tasks"
)

func TestScheduledAndManualRecursiveHousekeep(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "scheduled", true: "manual"}[manual], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "child", "old.log")
			now := time.Now().Truncate(time.Second)
			if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			past := now.Add(-72 * time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			reported := make(chan protocol.Result, 1)
			sender := reportFunc(func(_ context.Context, _ string, result protocol.Result) (protocol.Ack, error) {
				reported <- result
				return protocol.Ack{}, nil
			})
			engine, box, cancel := newEngineTest(t, sender, tasks.Executor{Now: func() time.Time { return now }})
			defer cancel()
			group := sampleSchedule()
			group.Details = group.Details[:1]
			group.Details[0].Task = protocol.Task{Type: "housekeep", DestinationPath: &root, HouseKeepDays: ptr(1), HouseKeepRecursive: true}
			snapshot := &protocol.Snapshot{Schedules: []protocol.Schedule{group}}
			if manual {
				snapshot.Schedules[0].Cron.Year = "1970"
				snapshot.Manuals = []protocol.Manual{{ID: 41, ScheduleID: group.ID, DetailID: group.Details[0].ID, Status: "wait", Claimable: true}}
				engine.Claim = func(context.Context, int64) (string, error) { return "claim", nil }
			}
			if err := engine.Apply(snapshot, now); err != nil {
				t.Fatal(err)
			}
			engine.Tick(now.Add(time.Second))
			select {
			case result := <-reported:
				if result.Code != 0 {
					t.Fatal(result)
				}
				if (result.ManualID != nil) != manual {
					t.Fatal("wrong execution mode", result.ManualID)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("housekeeping did not execute")
			}
			waitEmpty(t, box)
			engine.Stop()
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if !engine.Wait(ctx) {
				t.Fatal("engine did not stop")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("nested expired file retained", err)
			}
			if _, err := os.Stat(filepath.Dir(path)); err != nil {
				t.Fatal("directory removed", err)
			}
		})
	}
}
