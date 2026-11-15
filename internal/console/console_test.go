package console

import (
 "bytes"
 "context"
 "errors"
 "io"
 "os"
 "strings"
 "testing"
 "time"
)

func TestLineAndDefault(t *testing.T) {
 r, w, err := os.Pipe(); if err != nil { t.Fatal(err) }; defer r.Close()
 _, _ = io.WriteString(w, "\r\n  batch-01 \r\n"); _ = w.Close()
 var out bytes.Buffer
 c := New(r, &out)
 a, err := c.Line(context.Background(), "Server", "https://example.test")
 if err != nil || a != "https://example.test" { t.Fatal(a, err) }
 b, err := c.Line(context.Background(), "Device", "host")
 if err != nil || b != "batch-01" { t.Fatal(b, err) }
 if _, err := c.Line(context.Background(), "EOF", ""); !errors.Is(err, io.EOF) { t.Fatal(err) }
}
func TestSecretRefusesRedirectedInput(t *testing.T) {
 r, w, _ := os.Pipe(); defer r.Close(); defer w.Close()
 var out bytes.Buffer
 c := New(r, &out)
 if c.Interactive() { t.Fatal("pipe is not interactive") }
 if _, err := c.Secret(context.Background(), "Token"); err == nil { t.Fatal("secret accepted from a pipe") }
 if strings.Contains(out.String(), "Token") { t.Fatal("prompt should not claim hidden input on a pipe") }
}
func TestCancelledRead(t *testing.T) {
 r, w, _ := os.Pipe(); defer r.Close(); defer w.Close()
 var out bytes.Buffer
 c := New(r, &out)
 ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond); defer cancel()
 if _, err := c.Line(ctx, "Server", ""); !errors.Is(err, context.DeadlineExceeded) { t.Fatal(err) }
}
