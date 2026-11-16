package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
)

func TestContractHeadersAndBasePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/time_weaver/api/agent/v1/heartbeat" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-TW-Schema-Version") != "1" || r.Header.Get("X-TW-Agent-Capabilities") != "housekeep_recursive_v1" {
			t.Error(r.URL, r.Header)
		}
		fmt.Fprint(w, `{"schema_version":"1","server_time":"2026-08-01T05:00:00Z","data":{"ok":true}}`)
	}))
	defer server.Close()
	c := config.Defaults()
	c.API.BaseURL = server.URL + "/time_weaver"
	_, _, _, e := New(c, "test").Request(context.Background(), "POST", "/heartbeat", "secret", map[string]string{"state": "HEALTHY"}, nil)
	if e != nil {
		t.Fatal(e)
	}
}
func TestNoBlindPOSTRetryAndErrorRedaction(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"code":"unavailable","message":"SECRET_RESPONSE_TOKEN"}}`)
	}))
	defer server.Close()
	c := config.Defaults()
	c.API.BaseURL = server.URL
	_, _, _, e := New(c, "test").Request(context.Background(), "POST", "/token", "", nil, nil)
	if count.Load() != 1 || e == nil || !Retryable(e) || strings.Contains(e.Error(), "SECRET") {
		t.Fatal(count.Load(), e)
	}
	if e.(*Error).RetryAfter.Seconds() != 3 {
		t.Fatal(e)
	}
}
func TestRedirectIsNotFollowed(t *testing.T) {
	var hit atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	c := config.Defaults()
	c.API.BaseURL = source.URL
	_, _, _, e := New(c, "test").Request(context.Background(), "GET", "/snapshot", "secret", nil, nil)
	if e == nil || hit.Load() != 0 {
		t.Fatal("redirect leaked request", e)
	}
}
func TestMalformedEnvelope(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `not json`, `{"schema_version":"2","server_time":"2026-01-01T00:00:00Z","data":{}}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			c := config.Defaults()
			c.API.BaseURL = s.URL
			if _, _, _, e := New(c, "test").Request(context.Background(), "GET", "/snapshot", "", nil, nil); e == nil {
				t.Fatal("accepted")
			}
		})
	}
}
