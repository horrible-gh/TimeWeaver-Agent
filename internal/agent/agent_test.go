package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/cron"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

type tokens struct{}

func (tokens) Token(context.Context) (string, error) { return "access", nil }

type reportFunc func(context.Context, string, protocol.Result) (protocol.Ack, error)

func (f reportFunc) Report(c context.Context, t string, r protocol.Result) (protocol.Ack, error) {
	return f(c, t, r)
}

type runFunc func(context.Context, protocol.Task) (int, *string)

func (f runFunc) Run(c context.Context, t protocol.Task) (int, *string) { return f(c, t) }
func ptr[T any](v T) *T                                                 { return &v }
func cfg() config.Config {
	c := config.Defaults()
	c.Device = "batch-01"
	c.API.RetryInitialDelay = 0.01
	c.API.RetryMaxDelay = 0.02
	c.API.RetryJitterRatio = 0
	c.API.ManualDispatchDelay = 0
	return c
}
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}
func waitEmpty(t *testing.T, b *Outbox) {
	t.Helper()
	ctx, c := context.WithTimeout(context.Background(), 4*time.Second)
	defer c()
	if !b.WaitEmpty(ctx) {
		t.Fatal("outbox not empty", b.Size())
	}
}
func TestStateFailClosedAndTransientRecovery(t *testing.T) {
	s := NewState()
	if s.View().ExecutionAllowed {
		t.Fatal("bootstrap executed")
	}
	s.Snapshot()
	s.Failure("heartbeat", &api.Error{Code: "unavailable"})
	if v := s.View(); v.State != "DEGRADED" || !v.ExecutionAllowed {
		t.Fatal(v)
	}
	s.Failure("credential", &api.Error{Code: "needs_enrollment"})
	if s.View().State != "HALTED" || s.Valid() {
		t.Fatal(s.View())
	}
	s.Success("credential")
	if s.View().ExecutionAllowed {
		t.Fatal("must reacquire snapshot")
	}
	s.Snapshot()
	s.Success("heartbeat")
	if s.View().State != "HEALTHY" {
		t.Fatal(s.View())
	}
	s.Device("inactive")
	s.Device("active")
	if s.View().ExecutionAllowed {
		t.Fatal("inactive recovery reused stale snapshot")
	}
}
func TestOutboxFIFOStableKeysAndRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := NewState()
	state.Snapshot()
	var mu sync.Mutex
	var order, keys []string
	done := make(chan struct{})
	count := 0
	sender := reportFunc(func(ctx context.Context, token string, r protocol.Result) (protocol.Ack, error) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, r.DetailID)
		keys = append(keys, r.Key())
		if len(order) < 3 {
			return protocol.Ack{}, &api.Error{Code: "unavailable"}
		}
		return protocol.Ack{}, nil
	})
	box := NewOutbox(ctx, cfg().API, state, sender, tokens{})
	box.Ack = func(protocol.Result, protocol.Ack) {
		mu.Lock()
		defer mu.Unlock()
		count++
		if count == 2 {
			close(done)
		}
	}
	if !box.Reserve(2) {
		t.Fatal("reserve")
	}
	box.Enqueue(protocol.Result{ExecutionID: "g", DetailID: "one", Attempt: 1})
	box.Enqueue(protocol.Result{ExecutionID: "g", DetailID: "two", Attempt: 1})
	await(t, done)
	waitEmpty(t, box)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "one,one,one,two" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatal(order, keys)
	}
}
func TestOutboxCapacityAndPermanentRejection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := cfg().API
	c.OutboxCapacity = 3
	c.OutboxHighWatermark = 2
	c.OutboxLowWatermark = 1
	state := NewState()
	state.Snapshot()
	rejected := make(chan struct{})
	box := NewOutbox(ctx, c, state, reportFunc(func(context.Context, string, protocol.Result) (protocol.Ack, error) {
		return protocol.Ack{}, &api.Error{Code: "invalid_request"}
	}), tokens{})
	box.Reject = func(protocol.Result, string) { close(rejected) }
	if !box.Reserve(2) || box.Reserve(2) || box.Size() != 2 {
		t.Fatal("non-atomic admission")
	}
	if state.View().State != "DEGRADED" {
		t.Fatal("watermark")
	}
	box.Release(1)
	if state.View().State != "HEALTHY" {
		t.Fatal("watermark recovery")
	}
	box.Enqueue(protocol.Result{ExecutionID: "g", DetailID: "one", Attempt: 1})
	await(t, rejected)
	waitEmpty(t, box)
}
func sampleSchedule() protocol.Schedule {
	return protocol.Schedule{ID: 12, Name: "nightly", Cron: cron.EverySecond(), Details: []protocol.Detail{{ID: "00000000-0000-0000-0000-000000000001", Name: "first", Cron: cron.EverySecond(), Sequence: 1, ExecSequence: 1, Task: protocol.Task{Type: "command", Command: ptr("first")}}, {ID: "00000000-0000-0000-0000-000000000002", Name: "second", Cron: cron.EverySecond(), Sequence: 2, ExecSequence: 2, Task: protocol.Task{Type: "command", Command: ptr("second")}}}}
}
func newEngineTest(t *testing.T, sender Delivery, runner Executor) (*Engine, *Outbox, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := NewState()
	s.Snapshot()
	b := NewOutbox(ctx, cfg().API, s, sender, tokens{})
	e := NewEngine(ctx, cfg(), s, b, runner)
	return e, b, cancel
}
func TestSequenceWaitsForAckAndNoTaskRerun(t *testing.T) {
	t.Parallel()
	first := make(chan struct{})
	second := make(chan struct{})
	release := make(chan struct{})
	var executions atomic.Int32
	var reports atomic.Int32
	sender := reportFunc(func(ctx context.Context, token string, r protocol.Result) (protocol.Ack, error) {
		if r.DetailID == sampleSchedule().Details[0].ID {
			if reports.Add(1) == 1 {
				return protocol.Ack{}, &api.Error{Code: "unavailable"}
			}
			select {
			case <-release:
			case <-ctx.Done():
				return protocol.Ack{}, ctx.Err()
			}
		}
		return protocol.Ack{}, nil
	})
	runner := runFunc(func(ctx context.Context, task protocol.Task) (int, *string) {
		executions.Add(1)
		if *task.Command == "first" {
			close(first)
		} else {
			close(second)
		}
		return 0, nil
	})
	e, b, cancel := newEngineTest(t, sender, runner)
	defer cancel()
	now := time.Now().Truncate(time.Second)
	if err := e.Apply(&protocol.Snapshot{Schedules: []protocol.Schedule{sampleSchedule()}}, now); err != nil {
		t.Fatal(err)
	}
	e.Tick(now.Add(time.Second))
	e.Tick(now.Add(2 * time.Second))
	await(t, first)
	select {
	case <-second:
		t.Fatal("advanced before ACK")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	await(t, second)
	waitEmpty(t, b)
	if executions.Load() != 2 || reports.Load() != 2 {
		t.Fatal(executions.Load(), reports.Load())
	}
}
func TestSnapshotChangeStopsAfterCurrentSequence(t *testing.T) {
	t.Parallel()
	first := make(chan struct{})
	release := make(chan struct{})
	var count atomic.Int32
	runner := runFunc(func(ctx context.Context, task protocol.Task) (int, *string) {
		count.Add(1)
		close(first)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return 0, nil
	})
	e, b, cancel := newEngineTest(t, reportFunc(func(context.Context, string, protocol.Result) (protocol.Ack, error) { return protocol.Ack{}, nil }), runner)
	defer cancel()
	now := time.Now()
	g := sampleSchedule()
	if err := e.Apply(&protocol.Snapshot{Schedules: []protocol.Schedule{g}}, now); err != nil {
		t.Fatal(err)
	}
	e.Tick(now.Add(time.Second))
	await(t, first)
	g.Name = "changed"
	if err := e.Apply(&protocol.Snapshot{Schedules: []protocol.Schedule{g}}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitEmpty(t, b)
	time.Sleep(50 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatal("old sequence continued")
	}
}
func TestManualIsClaimedOnce(t *testing.T) {
	t.Parallel()
	var claims, executions atomic.Int32
	done := make(chan struct{})
	runner := runFunc(func(context.Context, protocol.Task) (int, *string) { executions.Add(1); return 0, nil })
	e, b, cancel := newEngineTest(t, reportFunc(func(_ context.Context, _ string, r protocol.Result) (protocol.Ack, error) {
		if r.ManualID == nil || *r.ManualID != 41 || r.ClaimToken == nil || *r.ClaimToken != "claim" {
			t.Error("missing manual context")
		}
		close(done)
		return protocol.Ack{}, nil
	}), runner)
	defer cancel()
	e.Claim = func(context.Context, int64) (string, error) { claims.Add(1); return "claim", nil }
	g := sampleSchedule()
	g.Cron.Year = "1970"
	now := time.Now()
	s := &protocol.Snapshot{Schedules: []protocol.Schedule{g}, Manuals: []protocol.Manual{{ID: 41, ScheduleID: 12, DetailID: g.Details[0].ID, Status: "wait", Claimable: true}}}
	if err := e.Apply(s, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		e.Tick(now.Add(time.Duration(i) * time.Second))
	}
	await(t, done)
	waitEmpty(t, b)
	e.Tick(now.Add(time.Minute))
	if claims.Load() != 1 || executions.Load() != 1 {
		t.Fatal(claims.Load(), executions.Load())
	}
}
func TestInvalidApplyKeepsOldSchedule(t *testing.T) {
	e, _, cancel := newEngineTest(t, reportFunc(func(context.Context, string, protocol.Result) (protocol.Ack, error) { return protocol.Ack{}, nil }), runFunc(func(context.Context, protocol.Task) (int, *string) { return 0, nil }))
	defer cancel()
	g := sampleSchedule()
	s := &protocol.Snapshot{Revision: "old", Schedules: []protocol.Schedule{g}}
	if err := e.Apply(s, time.Now()); err != nil {
		t.Fatal(err)
	}
	g.Cron.Minute = "bad"
	if e.Apply(&protocol.Snapshot{Revision: "bad", Schedules: []protocol.Schedule{g}}, time.Now()) == nil {
		t.Fatal("accepted")
	}
	current, _ := e.Snapshot()
	if current.Revision != "old" {
		t.Fatal("partial apply")
	}
}
func TestRuntimeEnrollmentExecutionRetryAndRestart(t *testing.T) {
	dir := t.TempDir()
	c := cfg()
	c.API.CredentialPath = filepath.Join(dir, "credential.json")
	c.API.StatusPath = filepath.Join(dir, "status.json")
	c.API.HeartbeatInterval = .05
	c.API.SnapshotSyncInterval = .05
	c.API.ShutdownGrace = 2
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "destination")
	_ = os.WriteFile(src, []byte("payload"), 0600)
	g := sampleSchedule()
	g.Details = g.Details[:1]
	g.Details[0].Task = protocol.Task{Type: "copy", SourcePath: &src, DestinationPath: &dst, ErrorOnMissingSource: true}
	var enrolls, refreshes, starts, results atomic.Int32
	var empty atomic.Bool
	finished := make(chan struct{})
	var once sync.Once
	respond := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "1", "server_time": time.Now().UTC(), "data": data})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/time_weaver/api/agent/v1")
		switch {
		case path == "/enroll" || path == "/token":
			if path == "/enroll" {
				enrolls.Add(1)
			} else {
				refreshes.Add(1)
			}
			now := time.Now().UTC()
			respond(w, map[string]any{"device_id": 7, "device_name": c.Device, "access_token": "access", "access_token_expires_at": now.Add(time.Hour), "refresh_token": "refresh", "refresh_token_expires_at": now.Add(24 * time.Hour)})
		case path == "/heartbeat":
			respond(w, map[string]any{"status": "active"})
		case path == "/snapshot":
			w.Header().Set("ETag", `W/"aaaaaaaaaaaaaaaa"`)
			if r.Header.Get("If-None-Match") != "" {
				w.WriteHeader(304)
				return
			}
			groups := []protocol.Schedule{g}
			if empty.Load() {
				groups = []protocol.Schedule{}
			}
			respond(w, protocol.Snapshot{Revision: "sha256:" + strings.Repeat("a", 64), GeneratedAt: time.Now().UTC(), Device: protocol.Device{Identity: protocol.Identity{DeviceID: 7, DeviceName: c.Device}, Status: "active"}, Schedules: groups, Manuals: []protocol.Manual{}})
		case strings.HasSuffix(path, "/start"):
			starts.Add(1)
			respond(w, map[string]any{})
		case strings.HasSuffix(path, "/results"):
			if results.Add(1) == 1 {
				w.WriteHeader(503)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unavailable"}})
				return
			}
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing idempotency key")
			}
			respond(w, map[string]any{"applied_transitions": []any{}})
			once.Do(func() { close(finished) })
		default:
			respond(w, map[string]any{})
		}
	}))
	defer server.Close()
	c.API.BaseURL = server.URL + "/time_weaver"
	t.Setenv(c.API.EnrollmentTokenEnv, "enrollment")
	runOnce := func(cancelWhen <-chan struct{}) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- New(c, "test").Run(ctx) }()
		select {
		case <-cancelWhen:
		case <-time.After(6 * time.Second):
			cancel()
			t.Fatal("runtime did not progress")
		}
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown hung")
		}
	}
	runOnce(finished)
	if enrolls.Load() != 1 || starts.Load() != 1 || results.Load() != 2 {
		t.Fatal(enrolls.Load(), starts.Load(), results.Load())
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "payload" {
		t.Fatal(string(b), err)
	}
	empty.Store(true)
	again := make(chan struct{})
	go func() { time.Sleep(150 * time.Millisecond); close(again) }()
	runOnce(again)
	if enrolls.Load() != 1 || refreshes.Load() != 1 {
		t.Fatal("restart re-enrolled", enrolls.Load(), refreshes.Load())
	}
	b, _ = os.ReadFile(c.API.StatusPath)
	if strings.Contains(string(b), "refresh_token") || strings.Contains(string(b), "access_token") {
		t.Fatal("status leaked secret")
	}
}
func TestStaleSnapshotCannotUndoRevocation(t *testing.T) {
	s := NewState()
	generation := s.Generation()
	s.Device("revoked")
	if s.AcceptSnapshot(generation) {
		t.Fatal("stale snapshot revived revoked device")
	}
	if s.View().ExecutionAllowed {
		t.Fatal("work allowed")
	}
	if !s.AcceptSnapshot(s.Generation()) {
		t.Fatal("fresh active snapshot must recover")
	}
}
