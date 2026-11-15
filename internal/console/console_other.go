//go:build !linux && !windows

package console

import (
 "os"
 "os/exec"
 "strings"
)

func stty(f *os.File, args ...string) (string, error) {
 cmd := exec.Command("stty", args...)
 cmd.Stdin = f
 b, err := cmd.Output()
 return strings.TrimSpace(string(b)), err
}
func isTerminal(f *os.File) bool { _, err := stty(f, "-g"); return err == nil }
func disableEcho(f *os.File) (func(), error) {
 old, err := stty(f, "-g")
 if err != nil { return nil, err }
 if _, err = stty(f, "-echo", "-echonl"); err != nil { return nil, err }
 return func() { _, _ = stty(f, old) }, nil
}
