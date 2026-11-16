package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecursiveHousekeepWireFlag(t *testing.T) {
	for _, value := range []string{"", `,"house_keep_recursive":false`, `,"house_keep_recursive":true`, `,"house_keep_recursive":"0"`, `,"house_keep_recursive":"1"`} {
		var task Task
		wire := `{"task_type":"housekeep","destination_path":"/cleanup","house_keep_days":1` + value + `}`
		if err := json.Unmarshal([]byte(wire), &task); err != nil {
			t.Fatal(err)
		}
		if err := task.Validate(); err != nil {
			t.Fatal(err)
		}
		want := strings.Contains(value, "true") || strings.HasSuffix(value, `"1"`)
		if bool(task.HouseKeepRecursive) != want {
			t.Fatal(value, task.HouseKeepRecursive)
		}
	}
	for _, value := range []string{`null`, `"maybe"`, `[]`, `2`} {
		var task Task
		if err := json.Unmarshal([]byte(`{"house_keep_recursive":`+value+`}`), &task); err == nil {
			t.Fatal("accepted invalid flag", value)
		}
	}
}
func TestRecursionRejectedForOtherTaskTypes(t *testing.T) {
	for _, kind := range []string{"command", "copy", "archive"} {
		task := Task{Type: kind, HouseKeepRecursive: true}
		if err := task.Validate(); err == nil {
			t.Fatal(kind)
		}
	}
}
func TestRecursiveFlagChangesAcceptedSnapshot(t *testing.T) {
	s := fixture()
	days := 1
	s.Schedules[0].Details[0].Task = Task{Type: "housekeep", DestinationPath: str("/cleanup"), HouseKeepDays: &days, HouseKeepRecursive: true}
	parsed, err := DecodeSnapshot(envelope(s), `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !bool(parsed.Schedules[0].Details[0].Task.HouseKeepRecursive) {
		t.Fatal("recursive flag lost")
	}
	e := envelope(s)
	e.Data = []byte(strings.Replace(string(e.Data), `,"house_keep_recursive":true`, "", 1))
	old, err := DecodeSnapshot(e, `W/"aaaaaaaaaaaaaaaa"`, Identity{7, "batch-01"})
	if err != nil {
		t.Fatal(err)
	}
	if bool(old.Schedules[0].Details[0].Task.HouseKeepRecursive) {
		t.Fatal("old server enabled recursion")
	}
}
