package tasks

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

func ptr[T any](v T) *T { return &v }
func TestCopyAndDateTemplate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in-20261114.txt")
	_ = os.WriteFile(src, []byte("hello"), 0600)
	dst := filepath.Join(dir, "nested", "out-{date}.txt")
	e := Executor{Now: func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }}
	code, msg := e.Run(context.Background(), protocol.Task{Type: "copy", SourcePath: ptr(filepath.Join(dir, "in-{date}.txt")), DestinationPath: &dst})
	if code != 0 {
		t.Fatal(msg)
	}
	b, err := os.ReadFile(filepath.Join(dir, "nested", "out-20261114.txt"))
	if err != nil || string(b) != "hello" {
		t.Fatal(string(b), err)
	}
}
func TestMissingSourcePolicy(t *testing.T) {
	dir := t.TempDir()
	task := protocol.Task{Type: "copy", SourcePath: ptr(filepath.Join(dir, "missing")), DestinationPath: ptr(filepath.Join(dir, "out"))}
	if c, _ := (Executor{}).Run(context.Background(), task); c != 0 {
		t.Fatal("should skip")
	}
	task.ErrorOnMissingSource = true
	if c, _ := (Executor{}).Run(context.Background(), task); c == 0 {
		t.Fatal("should fail")
	}
}
func TestSameFileCopyIsSafe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data")
	_ = os.WriteFile(p, []byte("keep"), 0600)
	if err := copyFile(context.Background(), p, p); err == nil {
		t.Fatal("same-file allowed")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "keep" {
		t.Fatal("data lost")
	}
}
func TestArchive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	_ = os.Mkdir(src, 0700)
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0600)
	out := filepath.Join(dir, "backup")
	task := protocol.Task{Type: "archive", SourcePath: &src, DestinationPath: &out, ArchiveType: ptr("zip")}
	if c, msg := (Executor{}).Run(context.Background(), task); c != 0 {
		t.Fatal(msg)
	}
	z, e := zip.OpenReader(out + ".zip")
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "a.txt" {
		t.Fatal(z.File)
	}
	r, _ := z.File[0].Open()
	defer r.Close()
	b, _ := io.ReadAll(r)
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
	if e = archive(context.Background(), src, filepath.Join(src, "self.zip")); e == nil {
		t.Fatal("self-inclusion allowed")
	}
}
func TestHousekeepAgeAndNoRecursiveDelete(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	recent := filepath.Join(dir, "recent")
	_ = os.WriteFile(old, []byte("old"), 0600)
	_ = os.WriteFile(recent, []byte("recent"), 0600)
	past := now.Add(-72 * time.Hour)
	_ = os.Chtimes(old, past, past)
	if e := Housekeep(context.Background(), dir, 1, now); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(old); !os.IsNotExist(e) {
		t.Fatal("old file retained")
	}
	if _, e := os.Stat(recent); e != nil {
		t.Fatal("recent lost")
	}
	nested := filepath.Join(dir, "nested")
	_ = os.Mkdir(nested, 0700)
	_ = os.WriteFile(filepath.Join(nested, "keep"), []byte("keep"), 0600)
	_ = os.Chtimes(nested, past, past)
	if e := Housekeep(context.Background(), dir, 1, now); e == nil {
		t.Fatal("old directory silently removed")
	}
	if _, e := os.Stat(filepath.Join(nested, "keep")); e != nil {
		t.Fatal(e)
	}
}
func TestHousekeepHugeRetentionDoesNotOverflow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "keep")
	_ = os.WriteFile(p, []byte("x"), 0600)
	if e := Housekeep(context.Background(), filepath.Dir(p), 1000000000, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(p); e != nil {
		t.Fatal("retention overflow deleted file")
	}
}
func TestCancelledTaskDoesNotWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	_ = os.WriteFile(src, []byte("x"), 0600)
	if c, _ := (Executor{}).Run(ctx, protocol.Task{Type: "copy", SourcePath: &src, DestinationPath: &dst}); c == 0 {
		t.Fatal("cancel ignored")
	}
	if _, e := os.Stat(dst); !os.IsNotExist(e) {
		t.Fatal("wrote after cancellation")
	}
}
func TestTemplateAndDateFormats(t *testing.T) {
	got, e := expand("{{x}}-{date}", "20261114")
	if e != nil || got != "{x}-20261114" {
		t.Fatal(got, e)
	}
	if _, e = expand("{unknown}", "d"); e == nil {
		t.Fatal("unknown accepted")
	}
	d := time.Date(2026, 11, 14, 21, 17, 8, 123456000, time.FixedZone("JST", 9*3600))
	if got = strftime(d, "%Y%m%d-%H%M%S-%f-%z"); got != "20261114-211708-123456-+0900" {
		t.Fatal(got)
	}
	if dateFormat(ptr("%H%M%S"), "%Y%m%d") != "%Y%m%d" {
		t.Fatal("Python fallback mismatch")
	}
}
func TestBoundedStderr(t *testing.T) {
	var b boundedBuffer
	_, _ = b.Write([]byte(strings.Repeat("x", 100000)))
	if len(b.String()) != 32768 {
		t.Fatal(len(b.String()))
	}
}
func TestCommandCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-tree behavior requires Windows runtime verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if e := command(ctx, "sleep 30"); e == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not cancel process tree")
	}
}
func TestDateValidationMatchesPythonReference(t *testing.T) {
	for _, format := range []string{"%Y", "%y", "%c", "%x", "%G-%V-%u", "%Y%j", "%Y-%W-%w", "%Y-%U-%w", "%Y%%", "%Y%f", "%Y %X"} {
		if got := dateFormat(&format, "fallback"); got != format {
			t.Error("valid format rejected", format, got)
		}
	}
	for _, format := range []string{"%G", "%Y-%m-%d %Z", "%Y%z", "%Y-%m-%d %g", "%Y %V", "%H%M%S"} {
		if got := dateFormat(&format, "fallback"); got != "fallback" {
			t.Error("invalid format accepted", format)
		}
	}
}
