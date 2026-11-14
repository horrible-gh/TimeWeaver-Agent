package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
)

func testConfig(t *testing.T) config.Config {
	c := config.Defaults()
	c.Device = "batch-01"
	c.API.CredentialPath = filepath.Join(t.TempDir(), "credential.json")
	return c
}
func seed(t *testing.T, c config.Config) {
	t.Helper()
	v := protocol.StoredCredential{SchemaVersion: "1", Identity: protocol.Identity{DeviceID: 7, DeviceName: c.Device}, RefreshToken: "old-refresh", RefreshTokenExpiresAt: time.Now().Add(time.Hour)}
	b, _ := json.Marshal(v)
	if e := securefile.Write(c.API.CredentialPath, b, true); e != nil {
		t.Fatal(e)
	}
}
func reply(w http.ResponseWriter, device string) {
	now := time.Now().UTC()
	_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "1", "server_time": now, "data": map[string]any{"device_id": 7, "device_name": device, "access_token": "new-access", "access_token_expires_at": now.Add(15 * time.Minute), "refresh_token": "new-refresh", "refresh_token_expires_at": now.Add(24 * time.Hour)}})
}
func TestSingleFlightRefreshAndDiskFormat(t *testing.T) {
	c := testConfig(t)
	seed(t, c)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/agent/v1/token" {
			t.Error(r.URL.Path)
		}
		reply(w, c.Device)
	}))
	defer s.Close()
	c.API.BaseURL = s.URL
	m := New(c, api.New(c, "test"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := m.Token(context.Background())
			if e != nil || token != "new-access" {
				t.Error(token, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	b, e := securefile.ReadPrivate(c.API.CredentialPath)
	if e != nil || strings.Contains(string(b), "new-access") {
		t.Fatal("access token persisted", e)
	}
	var stored protocol.StoredCredential
	if json.Unmarshal(b, &stored) != nil || stored.RefreshToken != "new-refresh" || stored.SchemaVersion != "1" {
		t.Fatal(string(b))
	}
}
func TestPersistenceFailureRetriesSameRotatedCredential(t *testing.T) {
	c := testConfig(t)
	seed(t, c)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, c.Device) }))
	defer s.Close()
	c.API.BaseURL = s.URL
	m := New(c, api.New(c, "test"))
	writes := 0
	m.Save = func(p string, b []byte, private bool) error {
		writes++
		if writes == 1 {
			return errors.New("disk full")
		}
		return securefile.Write(p, b, private)
	}
	if _, e := m.Token(context.Background()); api.Code(e) != "credential_persist_failed" {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(c.API.CredentialPath)
	if !strings.Contains(string(b), "old-refresh") {
		t.Fatal("old file destroyed")
	}
	if _, e := m.Token(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 || writes != 2 {
		t.Fatal("rotated twice", calls.Load(), writes)
	}
}
func TestExistingCredentialWinsOverConsumedEnrollmentToken(t *testing.T) {
	c := testConfig(t)
	seed(t, c)
	t.Setenv(c.API.EnrollmentTokenEnv, "already-consumed")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/enroll") {
			t.Error("must not reenroll")
		}
		reply(w, c.Device)
	}))
	defer s.Close()
	c.API.BaseURL = s.URL
	if _, e := New(c, api.New(c, "test")).Token(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestUnusableCredentialIsNeverDeleted(t *testing.T) {
	c := testConfig(t)
	_ = os.WriteFile(c.API.CredentialPath, []byte("broken"), 0600)
	m := New(c, api.New(c, "test"))
	if _, e := m.Token(context.Background()); api.Code(e) != "credential_invalid" {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(c.API.CredentialPath)
	if string(b) != "broken" {
		t.Fatal("credential deleted")
	}
}
func TestAutoEnrollmentOnce(t *testing.T) {
	c := testConfig(t)
	t.Setenv(c.API.EnrollmentTokenEnv, "enrollment-token")
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/enroll") {
			t.Error(r.URL)
		}
		reply(w, c.Device)
	}))
	defer s.Close()
	c.API.BaseURL = s.URL
	m := New(c, api.New(c, "test"))
	for i := 0; i < 2; i++ {
		if _, e := m.Token(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if e := m.Enroll(context.Background(), "another", false); e == nil {
		t.Fatal("implicit identity replacement")
	}
}
func TestRejectedRefreshPreservesFile(t *testing.T) {
	c := testConfig(t)
	seed(t, c)
	before, _ := os.ReadFile(c.API.CredentialPath)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"code":"invalid_token"}}`)
	}))
	defer s.Close()
	c.API.BaseURL = s.URL
	if _, e := New(c, api.New(c, "test")).Token(context.Background()); api.Code(e) != "needs_enrollment" {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(c.API.CredentialPath)
	if string(before) != string(after) {
		t.Fatal("file changed")
	}
}
func TestLateRejectionDoesNotInvalidateNewToken(t *testing.T) {
	c := testConfig(t)
	m := New(c, api.New(c, "test"))
	future := time.Now().Add(time.Hour)
	m.current = protocol.Credential{AccessToken: "new", AccessTokenExpiresAt: future}
	m.Reject("old")
	if !m.current.AccessTokenExpiresAt.Equal(future) {
		t.Fatal("old request invalidated new token")
	}
	m.Reject("new")
	if !m.current.AccessTokenExpiresAt.IsZero() {
		t.Fatal("rejected token retained")
	}
}
