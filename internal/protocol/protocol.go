// Package protocol owns the server's schema-1 wire model and validation.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/cron"
)

type Bool bool

func (v *Bool) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) > 1 && s[0] == '"' {
		var x string
		if err := json.Unmarshal(b, &x); err != nil {
			return err
		}
		s = strings.ToLower(strings.TrimSpace(x))
	}
	switch s {
	case "true", "1":
		*v = true
	case "false", "0":
		*v = false
	default:
		return errors.New("invalid boolean")
	}
	return nil
}

type Envelope struct {
	SchemaVersion string          `json:"schema_version"`
	ServerTime    time.Time       `json:"server_time"`
	Data          json.RawMessage `json:"data"`
}

func (e Envelope) Validate() error {
	if e.SchemaVersion != "1" {
		return errors.New("schema_mismatch")
	}
	if e.ServerTime.IsZero() || len(e.Data) == 0 || bytes.Equal(e.Data, []byte("null")) {
		return errors.New("malformed_response")
	}
	return nil
}

type Identity struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`
}
type Credential struct {
	Identity
	AccessToken           string    `json:"access_token"`
	AccessTokenExpiresAt  time.Time `json:"access_token_expires_at"`
	RefreshToken          string    `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at"`
}

func (c Credential) Validate(now time.Time) error {
	if c.AccessToken == "" || c.RefreshToken == "" || !c.AccessTokenExpiresAt.After(now) || !c.RefreshTokenExpiresAt.After(now) {
		return errors.New("invalid_credential_response")
	}
	return nil
}

type StoredCredential struct {
	SchemaVersion string `json:"schema_version"`
	Identity
	RefreshToken          string    `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at"`
}

func (c StoredCredential) Validate() error {
	if c.SchemaVersion != "1" || c.DeviceID < 1 || c.DeviceName == "" || c.RefreshToken == "" || c.RefreshTokenExpiresAt.IsZero() {
		return errors.New("invalid_credential_file")
	}
	return nil
}

type Device struct {
	Identity
	Status string `json:"status"`
}
type Task struct {
	Type                  string  `json:"task_type"`
	Command               *string `json:"command"`
	ArchiveType           *string `json:"archive_type"`
	SourcePath            *string `json:"source_path"`
	ErrorOnMissingSource  Bool    `json:"error_on_missing_source"`
	DestinationPath       *string `json:"destination_path"`
	DateFormat            *string `json:"date_format"`
	TargetDateFormat      *string `json:"target_date_format"`
	DestinationDateFormat *string `json:"destination_date_format"`
	HouseKeepDays         *int    `json:"house_keep_days"`
}
type Detail struct {
	ID           string      `json:"detail_id"`
	Name         string      `json:"schedule_name"`
	Cron         cron.Fields `json:"cron"`
	ErrorStop    Bool        `json:"is_error_stop"`
	Sequence     int         `json:"sequence"`
	ExecSequence int         `json:"exec_sequence"`
	RetryCount   int         `json:"retry_count"`
	Task         Task        `json:"task"`
}
type Schedule struct {
	ID        int64       `json:"schedule_id"`
	Name      string      `json:"name"`
	Cron      cron.Fields `json:"cron"`
	ErrorStop Bool        `json:"is_error_stop"`
	Details   []Detail    `json:"details"`
}
type Manual struct {
	ID          int64     `json:"manual_id"`
	ScheduleID  int64     `json:"schedule_id"`
	DetailID    string    `json:"detail_id"`
	Status      string    `json:"status"`
	Immediate   Bool      `json:"is_immediate"`
	ScheduledAt time.Time `json:"schedule_datetime"`
	Claimable   Bool      `json:"claimable"`
}

func (m Manual) Eligible() bool {
	return bool(m.Claimable) && m.Status != "done" && m.Status != "failed" && m.Status != "cancelled" && m.Status != "claimed"
}

type Snapshot struct {
	Revision    string     `json:"revision"`
	GeneratedAt time.Time  `json:"generated_at"`
	Device      Device     `json:"device"`
	Schedules   []Schedule `json:"schedules"`
	Manuals     []Manual   `json:"manual_runs"`
	ETag        string     `json:"-"`
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var revisionRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func UTC(t time.Time) bool { _, offset := t.Zone(); return !t.IsZero() && offset == 0 }
func require(raw json.RawMessage, names ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("expected object")
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return nil, fmt.Errorf("missing %s", name)
		}
	}
	return fields, nil
}
func array(raw json.RawMessage) ([]json.RawMessage, error) {
	var a []json.RawMessage
	err := json.Unmarshal(raw, &a)
	if err != nil || a == nil {
		return nil, errors.New("expected array")
	}
	return a, nil
}
func requireCron(raw json.RawMessage) error {
	_, err := require(raw, "year", "month", "day_of_week", "day", "hour", "minute", "second")
	return err
}

// DecodeSnapshot validates the complete document before any scheduler mutation.
func DecodeSnapshot(e Envelope, etag string, identity Identity) (*Snapshot, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if !UTC(e.ServerTime) {
		return nil, errors.New("server_time must be UTC")
	}
	f, err := require(e.Data, "revision", "generated_at", "device", "schedules")
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err = json.Unmarshal(e.Data, &s); err != nil {
		return nil, errors.New("invalid snapshot field type")
	}
	if !revisionRE.MatchString(s.Revision) || etag != `W/"`+strings.TrimPrefix(s.Revision, "sha256:")[:min(16, len(strings.TrimPrefix(s.Revision, "sha256:")))]+`"` {
		return nil, errors.New("revision/ETag mismatch")
	}
	if !UTC(s.GeneratedAt) || s.Device.Identity != identity || s.Device.Status != "active" {
		return nil, errors.New("snapshot time or device mismatch")
	}
	groups, err := array(f["schedules"])
	if err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	details := map[string]bool{}
	pairs := map[int64]map[string]bool{}
	for i, g := range s.Schedules {
		gf, err := require(groups[i], "schedule_id", "name", "cron", "is_error_stop", "details")
		if err != nil {
			return nil, err
		}
		if err = requireCron(gf["cron"]); err != nil {
			return nil, err
		}
		if _, err = cron.Compile(g.Cron); err != nil {
			return nil, err
		}
		if g.ID < 1 || g.Name == "" || ids[g.ID] {
			return nil, errors.New("invalid or duplicate schedule")
		}
		ids[g.ID] = true
		pairs[g.ID] = map[string]bool{}
		ds, err := array(gf["details"])
		if err != nil {
			return nil, err
		}
		for j, d := range g.Details {
			df, err := require(ds[j], "detail_id", "schedule_name", "cron", "is_error_stop", "sequence", "exec_sequence", "retry_count", "task")
			if err != nil {
				return nil, err
			}
			for _, name := range []string{"sequence", "exec_sequence", "retry_count"} {
				if bytes.Equal(bytes.TrimSpace(df[name]), []byte("null")) {
					return nil, fmt.Errorf("%s must be an integer", name)
				}
			}
			if err = requireCron(df["cron"]); err != nil {
				return nil, err
			}
			if _, err = cron.Compile(d.Cron); err != nil {
				return nil, err
			}
			if !uuidRE.MatchString(d.ID) || details[d.ID] || d.Name == "" || d.Sequence < 0 || d.ExecSequence < 1 || d.RetryCount < 0 {
				return nil, errors.New("invalid or duplicate detail")
			}
			if _, err = require(df["task"], "task_type", "command", "archive_type", "source_path", "error_on_missing_source", "destination_path", "date_format", "target_date_format", "destination_date_format", "house_keep_days"); err != nil {
				return nil, err
			}
			if err = d.Task.Validate(); err != nil {
				return nil, err
			}
			details[d.ID] = true
			pairs[g.ID][d.ID] = true
		}
		sorted := append([]Detail{}, g.Details...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Sequence == sorted[j].Sequence {
				return sorted[i].ID < sorted[j].ID
			}
			return sorted[i].Sequence < sorted[j].Sequence
		})
		for j := 1; j < len(sorted); j++ {
			if sorted[j-1].ExecSequence > sorted[j].ExecSequence {
				return nil, errors.New("exec_sequence must be nondecreasing")
			}
		}
	}
	mids := map[int64]bool{}
	if raw, ok := f["manual_runs"]; ok {
		ms, err := array(raw)
		if err != nil {
			return nil, err
		}
		for i, m := range s.Manuals {
			if _, err = require(ms[i], "manual_id", "schedule_id", "detail_id", "status", "is_immediate", "schedule_datetime", "claimable"); err != nil {
				return nil, err
			}
			if m.ID < 1 || m.ScheduleID < 1 || m.Status == "" || !uuidRE.MatchString(m.DetailID) || mids[m.ID] || !UTC(m.ScheduledAt) || !pairs[m.ScheduleID][m.DetailID] {
				return nil, errors.New("invalid manual execution")
			}
			mids[m.ID] = true
		}
	}
	s.ETag = etag
	return &s, nil
}
func (t Task) Validate() error {
	if t.HouseKeepDays != nil && *t.HouseKeepDays < 0 {
		return errors.New("negative house_keep_days")
	}
	cmd, arc, src, dst, days := t.Command != nil, t.ArchiveType != nil, t.SourcePath != nil, t.DestinationPath != nil, t.HouseKeepDays != nil
	valid := false
	switch t.Type {
	case "command":
		valid = cmd && !arc && !src && !dst && !days
	case "copy":
		valid = src && dst && !cmd && !arc && !days
	case "archive":
		valid = arc && src && dst && !cmd && !days
	case "housekeep":
		valid = dst && days && !cmd && !arc && !src
	}
	if !valid {
		return errors.New("task fields inconsistent with task_type")
	}
	return nil
}

type Result struct {
	ExecutionID string            `json:"execution_grp_id"`
	ScheduleID  int64             `json:"schedule_id"`
	DetailID    string            `json:"detail_id"`
	Attempt     int               `json:"attempt"`
	ManualID    *int64            `json:"manual_id"`
	ClaimToken  *string           `json:"claim_token"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at"`
	Code        int               `json:"result_code"`
	Message     *string           `json:"result_message"`
	Environment map[string]string `json:"environment_info"`
}

func (r Result) Key() string { return fmt.Sprintf("%s:%s:%d", r.ExecutionID, r.DetailID, r.Attempt) }

type Transition struct {
	Target string          `json:"target"`
	ID     json.RawMessage `json:"id"`
}
type Ack struct {
	Transitions []Transition `json:"applied_transitions"`
}
