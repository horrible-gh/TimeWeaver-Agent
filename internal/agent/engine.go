package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/user"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/cron"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

type Executor interface {
	Run(context.Context, protocol.Task) (int, *string)
}
type prepared struct {
	value       protocol.Schedule
	cron        *cron.Schedule
	fingerprint string
	last        time.Time
	disabled    bool
}
type execution struct {
	id             string
	group          int64
	sequences      map[int][]protocol.Detail
	keys           []int
	index, pending int
	stopped        bool
	excluded       map[string]bool
	manualID       *int64
	claim          *string
}
type dispatch struct {
	run    *execution
	detail protocol.Detail
}
type Engine struct {
	mu            sync.Mutex
	ctx           context.Context
	cfg           config.Config
	state         *State
	box           *Outbox
	runner        Executor
	start         func(context.Context, protocol.Result)
	groups        map[int64]*prepared
	snapshot      *protocol.Snapshot
	runs          map[string]*execution
	running       map[int64]string
	manualStarted map[int64]bool
	manualDue     map[int64]time.Time
	stopping      bool
	wg            sync.WaitGroup
	environment   map[string]string
	Claim         func(context.Context, int64) (string, error)
}

func NewEngine(ctx context.Context, c config.Config, state *State, box *Outbox, runner Executor) *Engine {
	e := &Engine{ctx: ctx, cfg: c, state: state, box: box, runner: runner, groups: map[int64]*prepared{}, runs: map[string]*execution{}, running: map[int64]string{}, manualStarted: map[int64]bool{}, manualDue: map[int64]time.Time{}, environment: Environment(c.Device)}
	box.Ack = e.ack
	box.Reject = e.reject
	state.OnHalt = e.Halt
	return e
}
func Environment(device string) map[string]string {
	host, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	osname := "posix"
	if runtime.GOOS == "windows" {
		osname = "nt"
	}
	ip := "127.0.0.1"
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addresses {
			if network, ok := addr.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
				ip = network.IP.String()
				break
			}
		}
	}
	return map[string]string{"host": host, "ip": ip, "os": osname, "user": username, "device_name": device}
}
func fingerprint(v protocol.Schedule) string { b, _ := json.Marshal(v); return string(b) }
func (e *Engine) Apply(s *protocol.Snapshot, now time.Time) error {
	// Own a deep copy: a running sequence must not observe a mutable snapshot.
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var copy protocol.Snapshot
	if err = json.Unmarshal(b, &copy); err != nil {
		return err
	}
	copy.ETag = s.ETag
	s = &copy
	next := map[int64]*prepared{}
	for _, g := range s.Schedules {
		compiled, err := cron.Compile(g.Cron)
		if err != nil {
			return err
		}
		next[g.ID] = &prepared{value: g, cron: compiled, fingerprint: fingerprint(g), last: now}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, g := range next {
		if old := e.groups[id]; old != nil && old.fingerprint == g.fingerprint {
			g.last = old.last
			g.disabled = old.disabled
		}
	}
	for _, r := range e.runs {
		if r.manualID == nil {
			old, new := e.groups[r.group], next[r.group]
			if new == nil || old == nil || old.fingerprint != new.fingerprint {
				r.stopped = true
			}
		}
	}
	seen := map[int64]bool{}
	for _, m := range s.Manuals {
		seen[m.ID] = true
		if m.Eligible() && !e.manualStarted[m.ID] {
			if _, ok := e.manualDue[m.ID]; !ok {
				e.manualDue[m.ID] = now.Add(config.Seconds(e.cfg.API.ManualDispatchDelay))
			}
		} else {
			delete(e.manualDue, m.ID)
		}
	}
	for id := range e.manualDue {
		if !seen[id] {
			delete(e.manualDue, id)
		}
	}
	for id := range e.manualStarted {
		if !seen[id] {
			active := false
			for _, r := range e.runs {
				if r.manualID != nil && *r.manualID == id {
					active = true
				}
			}
			if !active {
				delete(e.manualStarted, id)
			}
		}
	}
	e.groups = next
	e.snapshot = s
	return nil
}
func (e *Engine) Snapshot() (*protocol.Snapshot, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.snapshot == nil {
		return nil, ""
	}
	return e.snapshot, e.snapshot.ETag
}
func (e *Engine) Tick(now time.Time) {
	e.mu.Lock()
	var jobs []dispatch
	var manuals []protocol.Manual
	allowed := !e.stopping && e.state.View().ExecutionAllowed
	for id, g := range e.groups {
		_, due := g.cron.Latest(g.last, now, e.cfg.Location())
		if now.After(g.last) {
			g.last = now
		}
		if !allowed || !due || g.disabled || e.running[id] != "" || len(g.value.Details) == 0 {
			continue
		}
		runID, err := newUUID()
		if err != nil {
			slog.Error("cannot allocate execution id")
			continue
		}
		r := &execution{id: runID, group: id, sequences: map[int][]protocol.Detail{}, excluded: map[string]bool{}}
		for _, d := range g.value.Details {
			r.sequences[d.ExecSequence] = append(r.sequences[d.ExecSequence], d)
		}
		for seq := range r.sequences {
			r.keys = append(r.keys, seq)
		}
		sort.Ints(r.keys)
		e.runs[runID] = r
		e.running[id] = runID
		jobs = append(jobs, e.nextLocked(r)...)
	}
	if allowed && e.snapshot != nil && e.Claim != nil {
		for _, m := range e.snapshot.Manuals {
			due, ok := e.manualDue[m.ID]
			if ok && !now.Before(due) && !e.manualStarted[m.ID] && m.Eligible() {
				e.manualStarted[m.ID] = true
				delete(e.manualDue, m.ID)
				manuals = append(manuals, m)
				e.wg.Add(1)
			}
		}
	}
	e.mu.Unlock()
	e.launch(jobs)
	for _, m := range manuals {
		go e.manual(m)
	}
}
func (e *Engine) nextLocked(r *execution) []dispatch {
	if r.stopped || e.stopping || !e.state.View().ExecutionAllowed {
		e.finishLocked(r)
		return nil
	}
	for r.index < len(r.keys) {
		seq := r.keys[r.index]
		r.index++
		var ds []protocol.Detail
		for _, d := range r.sequences[seq] {
			if !r.excluded[d.ID] {
				ds = append(ds, d)
			}
		}
		if len(ds) == 0 {
			continue
		}
		if !e.box.Reserve(len(ds)) {
			e.finishLocked(r)
			return nil
		}
		r.pending = len(ds)
		e.wg.Add(len(ds))
		jobs := make([]dispatch, len(ds))
		for i, d := range ds {
			jobs[i] = dispatch{r, d}
		}
		return jobs
	}
	e.finishLocked(r)
	return nil
}
func (e *Engine) finishLocked(r *execution) {
	delete(e.runs, r.id)
	if r.manualID == nil {
		delete(e.running, r.group)
	}
}
func (e *Engine) launch(jobs []dispatch) {
	for _, j := range jobs {
		go e.runOne(j)
	}
}
func (e *Engine) runOne(j dispatch) {
	defer e.wg.Done()
	r, d := j.run, j.detail
	// Python dispatches sequence details one second after admission.
	_ = sleep(e.ctx, time.Second)
	result := protocol.Result{ExecutionID: r.id, ScheduleID: r.group, DetailID: d.ID, Attempt: 1, ManualID: r.manualID, ClaimToken: r.claim, StartedAt: time.Now().UTC(), Environment: e.environment}
	if e.ctx.Err() == nil && e.start != nil {
		ctx, cancel := context.WithTimeout(e.ctx, 1500*time.Millisecond)
		e.start(ctx, result)
		cancel()
	}
	result.Code, result.Message = e.runner.Run(e.ctx, d.Task)
	result.FinishedAt = time.Now().UTC()
	e.box.Enqueue(result)
	if result.Code != 0 {
		slog.Warn("task failed", "schedule_id", r.group, "detail_id", d.ID)
	}
}
func (e *Engine) manual(m protocol.Manual) {
	defer e.wg.Done()
	e.mu.Lock()
	var detail *protocol.Detail
	stillEligible := false
	if e.snapshot != nil {
		for _, current := range e.snapshot.Manuals {
			if current.ID == m.ID && current.ScheduleID == m.ScheduleID && current.DetailID == m.DetailID && current.Eligible() {
				stillEligible = true
			}
		}
	}
	if e.snapshot != nil {
		for _, g := range e.snapshot.Schedules {
			if g.ID == m.ScheduleID {
				for _, d := range g.Details {
					if d.ID == m.DetailID {
						copy := d
						detail = &copy
					}
				}
			}
		}
	}
	if !stillEligible || detail == nil || e.stopping || !e.state.View().ExecutionAllowed || !e.box.Reserve(1) {
		e.manualStarted[m.ID] = false
		e.manualDue[m.ID] = time.Now().Add(config.Seconds(e.cfg.API.SnapshotSyncInterval))
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	claim, err := e.Claim(e.ctx, m.ID)
	if err != nil {
		e.box.Release(1)
		e.mu.Lock()
		e.manualStarted[m.ID] = false
		e.manualDue[m.ID] = time.Now().Add(config.Seconds(e.cfg.API.SnapshotSyncInterval))
		e.mu.Unlock()
		return
	}
	id, err := newUUID()
	if err != nil {
		e.box.Release(1)
		return
	}
	r := &execution{id: id, group: m.ScheduleID, manualID: &m.ID, claim: &claim, pending: 1, excluded: map[string]bool{}}
	e.mu.Lock()
	blocked := e.stopping || !e.state.View().ExecutionAllowed
	if !blocked {
		e.runs[id] = r
		e.wg.Add(1)
	}
	e.mu.Unlock()
	if blocked {
		message := "Manual execution cancelled before dispatch."
		now := time.Now().UTC()
		e.box.Enqueue(protocol.Result{ExecutionID: id, ScheduleID: m.ScheduleID, DetailID: m.DetailID, Attempt: 1, ManualID: &m.ID, ClaimToken: &claim, StartedAt: now, FinishedAt: now, Code: -1, Message: &message, Environment: e.environment})
		return
	}
	e.launch([]dispatch{{r, *detail}})
}
func (e *Engine) ack(result protocol.Result, ack protocol.Ack) { e.complete(result, ack, false) }
func (e *Engine) reject(result protocol.Result, _ string)      { e.complete(result, protocol.Ack{}, true) }
func (e *Engine) complete(result protocol.Result, ack protocol.Ack, rejected bool) {
	e.mu.Lock()
	r := e.runs[result.ExecutionID]
	if r == nil {
		e.mu.Unlock()
		return
	}
	if rejected {
		r.stopped = true
	}
	for _, tr := range ack.Transitions {
		switch tr.Target {
		case "schedule_group":
			r.stopped = true
			if g := e.groups[r.group]; g != nil {
				g.disabled = true
			}
		case "schedule_detail":
			id := result.DetailID
			var value string
			if json.Unmarshal(tr.ID, &value) == nil && value != "" {
				id = value
			}
			r.excluded[id] = true
		}
	}
	r.pending--
	var jobs []dispatch
	if r.pending == 0 {
		if r.manualID != nil {
			e.finishLocked(r)
		} else {
			jobs = e.nextLocked(r)
		}
	}
	e.mu.Unlock()
	e.launch(jobs)
}
func (e *Engine) Halt() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range e.runs {
		r.stopped = true
	}
}
func (e *Engine) Stop() {
	e.mu.Lock()
	e.stopping = true
	for _, r := range e.runs {
		r.stopped = true
	}
	e.mu.Unlock()
}
func (e *Engine) Wait(ctx context.Context) bool {
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return false
	case <-done:
		return true
	}
}
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("random source unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
