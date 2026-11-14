package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExistingShapeAndDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(p, []byte(`{"device":"test","api":{"base_url":"https://example.test/time_weaver"},"reschedule":{"minute":"*/5"}}`), 0600)
	c, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	if c.API.OutboxCapacity != 10000 || c.API.SnapshotSyncInterval != 60 || c.Device != "test" {
		t.Fatal(c)
	}
}
func TestInvalidConfig(t *testing.T) {
	for _, url := range []string{"file:///tmp/a", "https://a/?token=x", "https://u:p@a", "https://a/#f", "relative"} {
		c := Defaults()
		c.API.BaseURL = url
		if c.Validate() == nil {
			t.Fatal(url)
		}
	}
	c := Defaults()
	c.API.OutboxHighWatermark = c.API.OutboxLowWatermark
	if c.Validate() == nil {
		t.Fatal("watermarks")
	}
}
func TestRejectTrailingJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(p, []byte(`{} {}`), 0600)
	if _, e := Load(p); e == nil {
		t.Fatal("extra object")
	}
}
func TestRejectNullConfig(t *testing.T) {
	for _, data := range []string{`null`, `{"api":null}`} {
		p := filepath.Join(t.TempDir(), "config.json")
		_ = os.WriteFile(p, []byte(data), 0600)
		if _, e := Load(p); e == nil {
			t.Fatal("accepted", data)
		}
	}
}
