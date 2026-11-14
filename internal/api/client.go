// Package api is an HTTP-only boundary. It owns neither files nor scheduling.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

const Prefix = "/api/agent/v1"
const maxResponse = 16 << 20

type Error struct {
	Code       string
	Status     int
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Code } // Never echo response bodies, tokens or URL credentials.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "local_error"
}
func Retryable(err error) bool {
	switch Code(err) {
	case "unavailable", "rate_limited", "server_error", "malformed_response":
		return true
	}
	return false
}
func AuthFailure(err error) bool {
	switch Code(err) {
	case "invalid_token", "token_expired", "needs_enrollment", "device_inactive", "device_revoked", "credential_persist_failed":
		return true
	}
	return false
}

type Client struct {
	Base, Version string
	HTTP          *http.Client
}

func New(c config.Config, version string) *Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: config.Seconds(c.API.ConnectTimeout), KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: config.Seconds(c.API.ConnectTimeout), ResponseHeaderTimeout: config.Seconds(c.API.ReadTimeout), MaxIdleConns: 16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second}
	return &Client{strings.TrimRight(c.API.BaseURL, "/"), version, &http.Client{Transport: tr, Timeout: config.Seconds(c.API.ConnectTimeout + c.API.ReadTimeout), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Request never blindly retries a token rotation or a potentially accepted
// manual claim. Retry policies live with their owners and keep idempotency keys.
func (c *Client) Request(ctx context.Context, method, path, token string, body any, headers map[string]string) (protocol.Envelope, http.Header, int, error) {
	var e protocol.Envelope
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return e, nil, 0, errors.New("encode request")
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+Prefix+path, rd)
	if err != nil {
		return e, nil, 0, errors.New("invalid request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-TW-Agent-Version", c.Version)
	req.Header.Set("X-TW-Schema-Version", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return e, nil, 0, ctx.Err()
		}
		return e, nil, 0, &Error{Code: "unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 {
		return e, resp.Header, resp.StatusCode, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(b) > maxResponse {
		return e, resp.Header, resp.StatusCode, &Error{Code: "malformed_response", Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		code := "invalid_request"
		switch {
		case resp.StatusCode == 401:
			code = "invalid_token"
		case resp.StatusCode == 403:
			code = "device_inactive"
		case resp.StatusCode == 404:
			code = "not_found"
		case resp.StatusCode == 409:
			code = "conflict"
		case resp.StatusCode == 429:
			code = "rate_limited"
		case resp.StatusCode >= 500:
			code = "server_error"
		}
		var wire struct {
			Error struct {
				Code       string  `json:"code"`
				RetryAfter float64 `json:"retry_after"`
			} `json:"error"`
			Detail struct {
				Code       string  `json:"code"`
				RetryAfter float64 `json:"retry_after"`
			} `json:"detail"`
		}
		_ = json.Unmarshal(b, &wire)
		detail := wire.Error
		if detail.Code == "" {
			detail = wire.Detail
		}
		if detail.Code != "" && len(detail.Code) <= 128 {
			code = detail.Code
		}
		delay := retryAfter(resp.Header.Get("Retry-After"))
		if delay == 0 && detail.RetryAfter > 0 && detail.RetryAfter <= 86400 {
			delay = config.Seconds(detail.RetryAfter)
		}
		return e, resp.Header, resp.StatusCode, &Error{Code: code, Status: resp.StatusCode, RetryAfter: delay}
	}
	if err = json.Unmarshal(b, &e); err != nil {
		return e, resp.Header, resp.StatusCode, &Error{Code: "malformed_response"}
	}
	if err = e.Validate(); err != nil {
		return e, resp.Header, resp.StatusCode, &Error{Code: err.Error()}
	}
	return e, resp.Header, resp.StatusCode, nil
}
func retryAfter(s string) time.Duration {
	if n, err := strconv.ParseFloat(s, 64); err == nil && n > 0 && n <= 86400 {
		return config.Seconds(n)
	}
	if t, err := http.ParseTime(s); err == nil {
		d := time.Until(t)
		if d > 0 && d <= 24*time.Hour {
			return d
		}
	}
	return 0
}
func (c *Client) Credential(ctx context.Context, path string, body any) (protocol.Credential, error) {
	e, _, _, err := c.Request(ctx, "POST", path, "", body, nil)
	var cred protocol.Credential
	if err != nil {
		return cred, err
	}
	if json.Unmarshal(e.Data, &cred) != nil {
		return cred, &Error{Code: "malformed_response"}
	}
	if err = cred.Validate(time.Now()); err != nil {
		return cred, &Error{Code: "malformed_response"}
	}
	return cred, nil
}
func (c *Client) Report(ctx context.Context, token string, r protocol.Result) (protocol.Ack, error) {
	var ack protocol.Ack
	e, _, _, err := c.Request(ctx, "POST", "/executions/"+r.ExecutionID+"/results", token, r, map[string]string{"Idempotency-Key": r.Key()})
	if err != nil {
		return ack, err
	}
	if json.Unmarshal(e.Data, &ack) != nil || len(e.Data) == 0 || e.Data[0] != '{' {
		return ack, &Error{Code: "malformed_response"}
	}
	return ack, nil
}
func (c *Client) Claim(ctx context.Context, token string, id int64) (string, error) {
	e, _, _, err := c.Request(ctx, "POST", fmt.Sprintf("/manual-runs/%d/claim", id), token, nil, nil)
	if err != nil {
		return "", err
	}
	var claim struct {
		Token string `json:"claim_token"`
	}
	if json.Unmarshal(e.Data, &claim) != nil || claim.Token == "" {
		return "", &Error{Code: "malformed_response"}
	}
	return claim.Token, nil
}
