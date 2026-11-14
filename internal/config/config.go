// Package config reads the existing Python agent's time_weaver.json contract.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

type API struct {
	BaseURL              string  `json:"base_url"`
	CredentialPath       string  `json:"credential_path"`
	StatusPath           string  `json:"status_path"`
	EnrollmentTokenEnv   string  `json:"enrollment_token_env"`
	ConnectTimeout       float64 `json:"connect_timeout"`
	ReadTimeout          float64 `json:"read_timeout"`
	HeartbeatInterval    float64 `json:"heartbeat_interval"`
	SnapshotSyncInterval float64 `json:"snapshot_sync_interval"`
	ShutdownGrace        float64 `json:"shutdown_grace"`
	OutboxCapacity       int     `json:"outbox_capacity"`
	OutboxHighWatermark  int     `json:"outbox_high_watermark"`
	OutboxLowWatermark   int     `json:"outbox_low_watermark"`
	OutboxSenderWorkers  int     `json:"outbox_sender_workers"`
	ManualDispatchDelay  float64 `json:"manual_dispatch_delay"`
	RetryInitialDelay    float64 `json:"retry_initial_delay"`
	RetryMultiplier      float64 `json:"retry_multiplier"`
	RetryMaxDelay        float64 `json:"retry_max_delay"`
	RetryJitterRatio     float64 `json:"retry_jitter_ratio"`
}

type Config struct {
	Device string `json:"device"`
	API    API    `json:"api"`
	// Kept for configuration compatibility. API intervals drive synchronization.
	Reschedule map[string]any `json:"reschedule,omitempty"`
	// Optional Go-agent extension. Empty uses the machine's local zone.
	Timezone string `json:"timezone,omitempty"`
}

func Defaults() Config {
	host, _ := os.Hostname()
	return Config{Device: host, API: API{
		BaseURL: "http://127.0.0.1:8000/time_weaver", CredentialPath: "conf/agent_credential.json",
		StatusPath: "log/agent_status.json", EnrollmentTokenEnv: "TIMEWEAVER_ENROLLMENT_TOKEN",
		ConnectTimeout: 5, ReadTimeout: 30, HeartbeatInterval: 30, SnapshotSyncInterval: 60, ShutdownGrace: 30,
		OutboxCapacity: 10000, OutboxHighWatermark: 8000, OutboxLowWatermark: 5000, OutboxSenderWorkers: 4,
		ManualDispatchDelay: 1, RetryInitialDelay: 1, RetryMultiplier: 2, RetryMaxDelay: 60, RetryJitterRatio: 0.2,
	}}
}

func Load(path string) (Config, error) {
	c := Defaults()
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	var raw json.RawMessage
	if err = dec.Decode(&raw); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return c, errors.New("config must be a JSON object")
	}
	if a, ok := fields["api"]; ok && strings.TrimSpace(string(a)) == "null" {
		return c, errors.New("api must be an object")
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return c, errors.New("config must contain one JSON object")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if strings.TrimSpace(c.Device) == "" {
		return errors.New("device is required")
	}
	u, err := url.Parse(c.API.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("api.base_url must be an HTTP(S) URL without credentials, query or fragment")
	}
	a := c.API
	if a.CredentialPath == "" || a.StatusPath == "" || a.EnrollmentTokenEnv == "" {
		return errors.New("credential_path, status_path and enrollment_token_env are required")
	}
	for name, v := range map[string]float64{"connect_timeout": a.ConnectTimeout, "read_timeout": a.ReadTimeout, "heartbeat_interval": a.HeartbeatInterval, "snapshot_sync_interval": a.SnapshotSyncInterval, "retry_initial_delay": a.RetryInitialDelay, "retry_max_delay": a.RetryMaxDelay} {
		if v <= 0 || v > 86400 {
			return fmt.Errorf("%s must be in (0,86400] seconds", name)
		}
	}
	if a.ShutdownGrace < 0 || a.ShutdownGrace > 86400 || a.ManualDispatchDelay < 0 || a.ManualDispatchDelay > 86400 {
		return errors.New("shutdown_grace/manual_dispatch_delay out of range")
	}
	if a.OutboxCapacity < 1 || a.OutboxLowWatermark < 0 || a.OutboxLowWatermark >= a.OutboxHighWatermark || a.OutboxHighWatermark > a.OutboxCapacity || a.OutboxSenderWorkers < 1 || a.OutboxSenderWorkers > 128 {
		return errors.New("invalid outbox capacity, watermarks or workers")
	}
	if a.RetryMultiplier < 1 || a.RetryMultiplier > 100 || a.RetryJitterRatio < 0 || a.RetryJitterRatio > 1 {
		return errors.New("invalid retry multiplier or jitter")
	}
	if c.Timezone != "" {
		if _, err = time.LoadLocation(c.Timezone); err != nil {
			return errors.New("invalid timezone")
		}
	}
	return nil
}
func Seconds(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }
func (c Config) Location() *time.Location {
	if c.Timezone != "" {
		loc, _ := time.LoadLocation(c.Timezone)
		if loc != nil {
			return loc
		}
	}
	return time.Local
}
