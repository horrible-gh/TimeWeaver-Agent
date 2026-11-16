package agent

import (
	"sort"
	"strings"
	"sync"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
)

type State struct {
	mu         sync.Mutex
	valid      bool
	generation uint64
	fatal      map[string]bool
	degraded   map[string]string
	OnHalt     func()
}
type StateView struct {
	State            string   `json:"state"`
	Reasons          []string `json:"reasons"`
	ExecutionAllowed bool     `json:"execution_allowed"`
}

func NewState() *State { return &State{fatal: map[string]bool{}, degraded: map[string]string{}} }
func (s *State) View() StateView {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := StateView{State: "BOOTSTRAP", Reasons: []string{}}
	if s.valid {
		v.State = "HEALTHY"
		if len(s.degraded) > 0 {
			v.State = "DEGRADED"
		}
	}
	if len(s.fatal) > 0 {
		v.State = "HALTED"
	}
	for r := range s.fatal {
		v.Reasons = append(v.Reasons, r)
	}
	for ch, r := range s.degraded {
		v.Reasons = append(v.Reasons, ch+":"+r)
	}
	sort.Strings(v.Reasons)
	v.ExecutionAllowed = v.State == "HEALTHY" || v.State == "DEGRADED"
	return v
}
func (s *State) Valid() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.valid }
func (s *State) Failure(channel string, err error) {
	code := api.Code(err)
	halt := api.AuthFailure(err) || strings.HasPrefix(code, "credential_")
	s.mu.Lock()
	wasHalted := len(s.fatal) > 0
	if halt {
		s.fatal[code] = true
		s.generation++
		s.valid = false
	} else {
		s.degraded[channel] = code
	}
	s.mu.Unlock()
	if halt && !wasHalted && s.OnHalt != nil {
		s.OnHalt()
	}
}
func (s *State) Success(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.degraded, channel)
	if channel == "credential" {
		for r := range s.fatal {
			if r == "needs_enrollment" || r == "invalid_token" || r == "token_expired" || strings.HasPrefix(r, "credential_") {
				delete(s.fatal, r)
			}
		}
	}
}
func (s *State) Snapshot() {
	s.mu.Lock()
	s.valid = true
	delete(s.degraded, "snapshot")
	s.mu.Unlock()
}
func (s *State) Device(status string) {
	if status == "active" {
		s.mu.Lock()
		delete(s.fatal, "device_inactive")
		delete(s.fatal, "device_revoked")
		s.mu.Unlock()
	} else if status == "inactive" || status == "revoked" {
		s.Failure("heartbeat", &api.Error{Code: "device_" + status})
	} else {
		s.Failure("heartbeat", &api.Error{Code: "device_status_unknown"})
	}
}
func (s *State) Backlog(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.degraded["outbox"] = "backlog"
	} else {
		delete(s.degraded, "outbox")
	}
}

// Generation fences in-flight snapshots against an intervening revocation.
func (s *State) Generation() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.generation }
func (s *State) AcceptSnapshot(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return false
	}
	delete(s.fatal, "device_inactive")
	delete(s.fatal, "device_revoked")
	delete(s.fatal, "agent_upgrade_required")
	s.valid = true
	delete(s.degraded, "snapshot")
	return true
}
