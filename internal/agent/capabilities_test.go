package agent

import (
	"testing"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
)

func TestCapabilityRefusalHaltsCachedWorkUntilFreshSnapshot(t *testing.T) {
	state := NewState()
	state.Snapshot()
	state.Failure("snapshot", &api.Error{Code: "agent_upgrade_required"})
	if state.Valid() || state.View().ExecutionAllowed {
		t.Fatal("cached execution continued")
	}
	state.Success("credential")
	state.Device("active")
	if state.View().ExecutionAllowed {
		t.Fatal("heartbeat cleared capability failure")
	}
	if !state.AcceptSnapshot(state.Generation()) || !state.View().ExecutionAllowed {
		t.Fatal("fresh supported snapshot did not recover")
	}
}
