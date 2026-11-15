// Package console provides small, cancellable setup prompts. Secrets are never
// echoed, including when stdin is redirected: callers must use flags/environment
// for unattended enrollment instead of silently falling back to visible input.
package console

import (
 "bufio"
 "context"
 "errors"
 "fmt"
 "io"
 "os"
 "strings"
)

type Console struct {
 input *os.File
 reader *bufio.Reader
 out io.Writer
}

func New(input *os.File, out io.Writer) *Console {
 return &Console{input: input, reader: bufio.NewReader(input), out: out}
}
func (c *Console) Interactive() bool { return isTerminal(c.input) }

func (c *Console) Line(ctx context.Context, label, fallback string) (string, error) {
 if fallback == "" { fmt.Fprintf(c.out, "%s: ", label) } else { fmt.Fprintf(c.out, "%s [%s]: ", label, fallback) }
 s, err := c.readLine(ctx)
 if err != nil { return "", err }
 if s == "" { s = fallback }
 return s, nil
}
func (c *Console) Secret(ctx context.Context, label string) (string, error) {
 if !c.Interactive() { return "", errors.New("a terminal is required for hidden token input; use --token or TIMEWEAVER_ENROLLMENT_TOKEN for unattended setup") }
 restore, err := disableEcho(c.input)
 if err != nil { return "", errors.New("cannot hide token input; use --token or TIMEWEAVER_ENROLLMENT_TOKEN instead") }
 defer restore()
 fmt.Fprintf(c.out, "%s (hidden): ", label)
 defer fmt.Fprintln(c.out)
 return c.readLine(ctx)
}
func (c *Console) readLine(ctx context.Context) (string, error) {
 type result struct { text string; err error }
 done := make(chan result, 1)
 go func() {
  s, err := c.reader.ReadString('\n')
  if err == io.EOF && len(s) > 0 { err = nil }
  done <- result{strings.TrimSpace(s), err}
 }()
 select {
 case <-ctx.Done(): return "", ctx.Err()
 case r := <-done: return r.text, r.err
 }
}
