//go:build linux

package console

import (
 "os"
 "syscall"
 "unsafe"
)

func terminalMode(f *os.File) (syscall.Termios, error) {
 var state syscall.Termios
 _, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
 if err != 0 { return state, err }
 return state, nil
}
func isTerminal(f *os.File) bool { _, err := terminalMode(f); return err == nil }
func disableEcho(f *os.File) (func(), error) {
 old, err := terminalMode(f)
 if err != nil { return nil, err }
 next := old
 next.Lflag &^= syscall.ECHO | syscall.ECHONL
 _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&next)))
 if errno != 0 { return nil, errno }
 return func() { syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&old))) }, nil
}
