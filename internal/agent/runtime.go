package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/credential"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/tasks"
)

type Runtime struct {
	cfg       config.Config
	client    *api.Client
	auth      *credential.Manager
	state     *State
	engine    *Engine
	outbox    *Outbox
	eventMu   sync.Mutex
	eventKeys map[string]bool
	previous  string
	work      context.Context
}

func New(c config.Config, version string) *Runtime {
	a := api.New(c, version)
	return &Runtime{cfg: c, client: a, auth: credential.New(c, a), state: NewState(), eventKeys: map[string]bool{}}
}
func (r *Runtime) Run(ctx context.Context) error {
	lock, err := securefile.Acquire(r.cfg.API.CredentialPath + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	work, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.work = work
	r.outbox = NewOutbox(work, r.cfg.API, r.state, r.client, r.auth)
	r.engine = NewEngine(work, r.cfg, r.state, r.outbox, tasks.Executor{})
	r.engine.start = func(ctx context.Context, result protocol.Result) {
		token, err := r.auth.Token(ctx)
		if err != nil {
			return
		}
		_, _, _, err = r.client.Request(ctx, "POST", "/executions/"+result.ExecutionID+"/start", token, map[string]any{"execution_grp_id": result.ExecutionID, "schedule_id": result.ScheduleID, "detail_id": result.DetailID, "attempt": 1, "started_at": result.StartedAt}, nil)
		if err != nil {
			slog.Warn("start notification failed", "code", api.Code(err))
		}
	}
	r.engine.Claim = func(ctx context.Context, id int64) (string, error) {
		token, err := r.token(ctx)
		if err != nil {
			return "", err
		}
		claim, err := r.client.Claim(ctx, token, id)
		if api.Code(err) == "invalid_token" || api.Code(err) == "token_expired" {
			r.auth.Reject(token)
		}
		if err != nil && api.AuthFailure(err) {
			r.state.Failure("manual", err)
		}
		return claim, err
	}
	r.outbox.Event = func(kind, severity, message string) { r.event(work, kind, severity, message) }
	var polls sync.WaitGroup
	polls.Add(2)
	go func() {
		defer polls.Done()
		r.loop(ctx, "heartbeat", config.Seconds(r.cfg.API.HeartbeatInterval), r.heartbeat)
	}()
	go func() {
		defer polls.Done()
		r.loop(ctx, "snapshot", config.Seconds(r.cfg.API.SnapshotSyncInterval), r.snapshot)
	}()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastStatus := time.Time{}
	slog.Info("TimeWeaver Agent started", "device", r.cfg.Device, "version", r.client.Version)
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case now := <-ticker.C:
			r.engine.Tick(now)
			if now.Sub(lastStatus) >= time.Second {
				r.status()
				lastStatus = now
			}
		}
	}
	r.engine.Stop()
	// Stop polling first; allow already admitted tasks and their result ACKs to
	// finish within one shared grace deadline, then cancel child process trees.
	grace, graceCancel := context.WithTimeout(context.Background(), config.Seconds(r.cfg.API.ShutdownGrace))
	defer graceCancel()
	finished := r.engine.Wait(grace)
	drained := r.outbox.WaitEmpty(grace)
	cancel()
	polls.Wait()
	cleanup, cleanupCancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cleanupCancel()
	_ = r.engine.Wait(cleanup)
	r.status()
	if !finished || !drained {
		slog.Warn("shutdown grace expired; in-memory results are not durable", "pending", r.outbox.Size())
	} else {
		slog.Info("TimeWeaver Agent stopped")
	}
	return nil
}
func (r *Runtime) token(ctx context.Context) (string, error) {
	token, err := r.auth.Token(ctx)
	if err != nil {
		r.state.Failure("credential", err)
	} else {
		r.state.Success("credential")
	}
	return token, err
}
func (r *Runtime) loop(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	attempt := 0
	lastCode := ""
	lastLog := time.Time{}
	for ctx.Err() == nil {
		err := fn(ctx)
		if ctx.Err() != nil {
			return
		}
		delay := interval
		if err != nil {
			r.state.Failure(name, err)
			code := api.Code(err)
			if code != lastCode || time.Since(lastLog) > time.Minute {
				slog.Warn(name+" failed", "code", code)
				lastCode = code
				lastLog = time.Now()
			}
			delay = retryDelay(r.cfg.API, attempt, err)
			attempt++
		} else {
			r.state.Success(name)
			attempt = 0
			if lastCode != "" {
				slog.Info(name + " recovered")
				lastCode = ""
			}
		}
		if !sleep(ctx, delay) {
			return
		}
	}
}
func (r *Runtime) heartbeat(ctx context.Context) error {
	token, err := r.token(ctx)
	if err != nil {
		return err
	}
	s, _ := r.engine.Snapshot()
	var rev *string
	if s != nil {
		v := s.Revision
		rev = &v
	}
	e, _, _, err := r.client.Request(ctx, "POST", "/heartbeat", token, map[string]any{"agent_version": r.client.Version, "state": r.state.View().State, "applied_revision": rev}, nil)
	if err != nil {
		if api.Code(err) == "invalid_token" || api.Code(err) == "token_expired" {
			r.auth.Reject(token)
		}
		return err
	}
	var data struct {
		Device struct {
			Status string `json:"status"`
		} `json:"device"`
		DeviceStatus string    `json:"device_status"`
		Status       string    `json:"status"`
		ServerTime   time.Time `json:"server_time"`
	}
	if json.Unmarshal(e.Data, &data) != nil {
		return &api.Error{Code: "malformed_response"}
	}
	status := data.Device.Status
	if status == "" {
		status = data.DeviceStatus
	}
	if status == "" {
		status = data.Status
	}
	if status != "" {
		r.state.Device(status)
	}
	skew := math.Abs(time.Since(e.ServerTime).Seconds())
	if skew > 300 {
		r.state.Failure("clock", &api.Error{Code: "clock_skew"})
	} else {
		r.state.Success("clock")
	}
	return nil
}
func (r *Runtime) snapshot(ctx context.Context) error {
	token, err := r.token(ctx)
	if err != nil {
		return err
	}
	generation := r.state.Generation()
	old, etag := r.engine.Snapshot()
	if !r.state.Valid() {
		etag = ""
	}
	headers := map[string]string{}
	if etag != "" {
		headers["If-None-Match"] = etag
	}
	e, h, status, err := r.client.Request(ctx, "GET", "/snapshot", token, nil, headers)
	if err != nil {
		if api.Code(err) == "invalid_token" || api.Code(err) == "token_expired" {
			r.auth.Reject(token)
		}
		return err
	}
	if err != nil {
		return err
	}
	if status == 304 {
		if old == nil || etag == "" || h.Get("ETag") != "" && h.Get("ETag") != etag {
			return &api.Error{Code: "malformed_response"}
		}
		return nil
	}
	s, err := protocol.DecodeSnapshot(e, h.Get("ETag"), r.auth.Identity())
	if err != nil {
		return &api.Error{Code: "invalid_snapshot"}
	}
	if err = r.engine.Apply(s, time.Now()); err != nil {
		return &api.Error{Code: "invalid_snapshot"}
	}
	if !r.state.AcceptSnapshot(generation) {
		return &api.Error{Code: "unavailable"}
	}
	return nil
}
func (r *Runtime) status() {
	view := r.state.View()
	s, _ := r.engine.Snapshot()
	var rev any
	if s != nil {
		rev = s.Revision
	}
	payload := map[string]any{"schema_version": "1", "updated_at": time.Now().UTC(), "agent_version": r.client.Version, "device": r.auth.Identity(), "state": view.State, "reasons": view.Reasons, "execution_allowed": view.ExecutionAllowed, "applied_revision": rev, "outbox_size": r.outbox.Size()}
	b, _ := json.MarshalIndent(payload, "", "  ")
	if err := securefile.Write(r.cfg.API.StatusPath, append(b, '\n'), false); err != nil {
		slog.Warn("cannot write status file")
	}
	if r.previous != view.State {
		slog.Info("agent state", "state", view.State, "reasons", view.Reasons)
		old := r.previous
		r.previous = view.State
		if old == "HEALTHY" && view.State == "DEGRADED" {
			r.clearEvent("recovered", "Agent recovered healthy operation.")
			go r.event(r.work, "degraded", "warning", "Agent entered degraded operation.")
		} else if old == "DEGRADED" && view.State == "HEALTHY" {
			r.clearEvent("degraded", "Agent entered degraded operation.")
			go r.event(r.work, "recovered", "info", "Agent recovered healthy operation.")
		}
	}
}
func (r *Runtime) event(ctx context.Context, kind, severity, message string) {
	key := kind + ":" + message
	r.eventMu.Lock()
	if kind != "outbox_backlog" && r.eventKeys[key] {
		r.eventMu.Unlock()
		return
	}
	r.eventKeys[key] = true
	r.eventMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	token, err := r.auth.Token(ctx)
	if err != nil {
		return
	}
	_, _, _, _ = r.client.Request(ctx, "POST", "/events", token, map[string]any{"event_type": kind, "severity": severity, "message": message, "occurred_at": time.Now().UTC(), "environment_info": Environment(r.cfg.Device)}, nil)
}

func (r *Runtime) clearEvent(kind, message string) {
	r.eventMu.Lock()
	delete(r.eventKeys, kind+":"+message)
	r.eventMu.Unlock()
}
