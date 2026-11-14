package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpVersionAndUnknown(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"version"}} {
		var out, err bytes.Buffer
		if code := run(args, &out, &err); code != 0 || out.Len() == 0 {
			t.Fatal(code, out.String(), err.String())
		}
	}
	var out, err bytes.Buffer
	if code := run([]string{"invalid"}, &out, &err); code != 2 {
		t.Fatal(code)
	}
}
func TestInitDoesNotOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conf", "time_weaver.json")
	args := []string{"init", "--config", p, "--server", "https://example.test/time_weaver", "--device", "batch-01"}
	var out, err bytes.Buffer
	if code := run(args, &out, &err); code != 0 {
		t.Fatal(code, err.String())
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "batch-01") || strings.Contains(string(b), "refresh_token") {
		t.Fatal(string(b))
	}
	out.Reset()
	err.Reset()
	if code := run(args, &out, &err); code == 0 {
		t.Fatal("overwrote existing config")
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(b) {
		t.Fatal("config changed")
	}
}
