// Package credential owns enrollment, single-flight rotation and persistence.
package credential

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
)

type Manager struct {
	mu          sync.Mutex
	client      *api.Client
	cfg         config.Config
	current     protocol.Credential
	pending     bool
	identity    protocol.Identity
	enrollTried bool
	Save        func(string, []byte, bool) error
}

func New(c config.Config, a *api.Client) *Manager {
	return &Manager{cfg: c, client: a, Save: securefile.Write}
}
func (m *Manager) Identity() protocol.Identity { m.mu.Lock(); defer m.mu.Unlock(); return m.identity }

// Reject invalidates only the access token actually rejected by the server.
// A late 401 for an older token must not invalidate a newer concurrent rotation.
func (m *Manager) Reject(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token == m.current.AccessToken {
		m.current.AccessTokenExpiresAt = time.Time{}
	}
}

func (m *Manager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending {
		if e := m.persist(); e != nil {
			return "", e
		}
	}
	if m.current.AccessToken != "" && time.Until(m.current.AccessTokenExpiresAt) > 60*time.Second {
		return m.current.AccessToken, nil
	}
	b, err := securefile.ReadPrivate(m.cfg.API.CredentialPath)
	if os.IsNotExist(err) {
		token := os.Getenv(m.cfg.API.EnrollmentTokenEnv)
		if token == "" || m.enrollTried {
			return "", &api.Error{Code: "needs_enrollment"}
		}
		m.enrollTried = true
		if err = m.enroll(ctx, token); err != nil {
			return "", err
		}
		_ = os.Unsetenv(m.cfg.API.EnrollmentTokenEnv)
		return m.current.AccessToken, nil
	}
	if err != nil {
		return "", &api.Error{Code: "credential_unreadable"}
	}
	var stored protocol.StoredCredential
	if json.Unmarshal(b, &stored) != nil || stored.Validate() != nil || stored.DeviceName != m.cfg.Device {
		return "", &api.Error{Code: "credential_invalid"}
	}
	m.identity = stored.Identity
	if !stored.RefreshTokenExpiresAt.After(time.Now()) {
		return "", &api.Error{Code: "needs_enrollment"}
	}
	next, err := m.client.Credential(ctx, "/token", map[string]string{"refresh_token": stored.RefreshToken})
	if err != nil {
		if api.Code(err) == "invalid_token" || api.Code(err) == "token_expired" {
			return "", &api.Error{Code: "needs_enrollment"}
		}
		return "", err
	}
	if next.DeviceID == 0 {
		next.DeviceID = stored.DeviceID
	}
	if next.DeviceName == "" {
		next.DeviceName = stored.DeviceName
	}
	if next.Identity != stored.Identity {
		return "", &api.Error{Code: "credential_identity_mismatch"}
	}
	m.current = next
	m.pending = true
	if err = m.persist(); err != nil {
		return "", err
	}
	return next.AccessToken, nil
}
func (m *Manager) Enroll(ctx context.Context, token string, replace bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token == "" {
		return errors.New("enrollment token environment variable is empty")
	}
	if _, e := os.Lstat(m.cfg.API.CredentialPath); e == nil && !replace {
		return errors.New("credential already exists; stop the old agent and explicitly use --replace")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	return m.enroll(ctx, token)
}
func (m *Manager) enroll(ctx context.Context, token string) error {
	next, e := m.client.Credential(ctx, "/enroll", map[string]string{"enrollment_token": token, "device_name": m.cfg.Device, "agent_version": m.client.Version})
	if e != nil {
		return e
	}
	if next.DeviceID < 1 || next.DeviceName != m.cfg.Device {
		return &api.Error{Code: "credential_identity_mismatch"}
	}
	m.identity = next.Identity
	m.current = next
	m.pending = true
	return m.persist()
}
func (m *Manager) persist() error {
	stored := protocol.StoredCredential{SchemaVersion: "1", Identity: m.current.Identity, RefreshToken: m.current.RefreshToken, RefreshTokenExpiresAt: m.current.RefreshTokenExpiresAt}
	b, e := json.MarshalIndent(stored, "", "  ")
	if e != nil {
		return &api.Error{Code: "credential_persist_failed"}
	}
	if e = m.Save(m.cfg.API.CredentialPath, append(b, '\n'), true); e != nil {
		return &api.Error{Code: "credential_persist_failed"}
	}
	m.identity = m.current.Identity
	m.pending = false
	return nil
}
