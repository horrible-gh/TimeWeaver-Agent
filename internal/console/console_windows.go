//go:build windows

package console

import (
 "os"
 "syscall"
)

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

func isTerminal(f *os.File) bool {
 var mode uint32
 return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
func disableEcho(f *os.File) (func(), error) {
 var old uint32
 if err := syscall.GetConsoleMode(syscall.Handle(f.Fd()), &old); err != nil { return nil, err }
 // Keep line input and processed input enabled, and disable ENABLE_ECHO_INPUT.
 next := (old | 0x0001 | 0x0002) &^ uint32(0x0004)
 ok, _, err := setConsoleMode.Call(f.Fd(), uintptr(next))
 if ok == 0 { return nil, err }
 return func() { setConsoleMode.Call(f.Fd(), uintptr(old)) }, nil
}
