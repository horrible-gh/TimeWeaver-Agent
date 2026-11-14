package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/cron"
)

func str(s string) *string { return &s }
func fixture() Snapshot {
	return Snapshot{Revision: "sha256:" + strings.Repeat("a", 64), GeneratedAt: time.Date(2026, 8, 1, 5, 10, 0, 0, time.UTC), Device: Device{Identity: Identity{7, "batch-01"}, Status: "active"}, Schedules: []Schedule{{ID: 12, Name: "nightly", Cron: cron.EverySecond(), Details: []Detail{{ID: "00000000-0000-0000-0000-00000000000c", Name: "copy-logs", Cron: cron.EverySecond(), Sequence: 10, ExecSequence: 1, Task: Task{Type: "copy", SourcePath: str("/data/log"), DestinationPath: str("/backup/log"), ErrorOnMissingSource: true}}}}}, Manuals: []Manual{{ID: 41, ScheduleID: 12, DetailID: "00000000-0000-0000-0000-00000000000c", Status: "wait", Claimable: true, ScheduledAt: time.Date(2026, 8, 1, 6, 0, 0, 0, time.UTC)}}}
}
func envelope(s Snapshot) Envelope {
	b, _ := json.Marshal(s)
	return Envelope{SchemaVersion: "1", ServerTime: s.GeneratedAt, Data: b}
}
func TestSnapshotContract(t *testing.T) {
	if _, e := DecodeSnapshot(envelope(fixture()), `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"}); e != nil {
		t.Fatal(e)
	}
}
func TestRejectWholeSnapshot(t *testing.T) {
	mutations := map[string]func(*Snapshot){"revision": func(s *Snapshot) { s.Revision = "bad" }, "identity": func(s *Snapshot) { s.Device.DeviceName = "other" }, "inactive": func(s *Snapshot) { s.Device.Status = "inactive" }, "duplicate_schedule": func(s *Snapshot) { s.Schedules = append(s.Schedules, s.Schedules[0]) }, "duplicate_detail": func(s *Snapshot) { s.Schedules[0].Details = append(s.Schedules[0].Details, s.Schedules[0].Details[0]) }, "foreign_manual": func(s *Snapshot) { s.Manuals[0].ScheduleID = 999 }, "bad_cron": func(s *Snapshot) { s.Schedules[0].Cron.Minute = "bad" }, "bad_task": func(s *Snapshot) { s.Schedules[0].Details[0].Task.Command = str("forbidden") }, "negative_retry": func(s *Snapshot) { s.Schedules[0].Details[0].RetryCount = -1 }, "non_utc": func(s *Snapshot) {
		s.Manuals[0].ScheduledAt = s.Manuals[0].ScheduledAt.In(time.FixedZone("JST", 9*3600))
	}, "exec_order": func(s *Snapshot) {
		a := s.Schedules[0].Details[0]
		a.ExecSequence = 2
		b := a
		b.ID = "00000000-0000-0000-0000-00000000000d"
		b.Sequence++
		b.ExecSequence = 1
		s.Schedules[0].Details = []Detail{a, b}
	}}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s := fixture()
			mutate(&s)
			if _, e := DecodeSnapshot(envelope(s), `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"}); e == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
}
func TestSchemaETagAndMissingField(t *testing.T) {
	e := envelope(fixture())
	e.SchemaVersion = "2"
	if _, err := DecodeSnapshot(e, `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"}); err == nil {
		t.Fatal("schema")
	}
	e.SchemaVersion = "1"
	if _, err := DecodeSnapshot(e, "wrong", Identity{7, "batch-01"}); err == nil {
		t.Fatal("etag")
	}
	e.Data = []byte(strings.Replace(string(e.Data), `"retry_count":0,`, "", 1))
	if _, err := DecodeSnapshot(e, `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"}); err == nil {
		t.Fatal("missing field")
	}
}
func TestBooleanCompatibility(t *testing.T) {
	for _, x := range []string{`true`, `false`, `1`, `0`, `"true"`, `"FALSE"`, `"1"`, `"0"`} {
		var v Bool
		if e := json.Unmarshal([]byte(x), &v); e != nil {
			t.Fatal(x, e)
		}
	}
	for _, x := range []string{`2`, `-1`, `"yes"`, `null`, `[]`, `{}`} {
		var v Bool
		if e := json.Unmarshal([]byte(x), &v); e == nil {
			t.Fatal(x)
		}
	}
}
func TestResultKeyStable(t *testing.T) {
	r := Result{ExecutionID: "exec", DetailID: "detail", Attempt: 1}
	if r.Key() != "exec:detail:1" {
		t.Fatal(r.Key())
	}
}
func TestNullRetryCountIsNotZero(t *testing.T) {
	e := envelope(fixture())
	e.Data = []byte(strings.Replace(string(e.Data), `"retry_count":0`, `"retry_count":null`, 1))
	if _, err := DecodeSnapshot(e, `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"}); err == nil {
		t.Fatal("null integer accepted")
	}
}
