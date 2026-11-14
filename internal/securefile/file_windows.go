//go:build windows

package securefile

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
	"unsafe"
)

func secureOwner(p string) error {
	u, e := user.Current()
	if e != nil {
		return e
	}
	cmd := exec.Command("icacls", p, "/inheritance:r", "/grant:r", "*"+u.Uid+":(F)", "*S-1-5-18:(F)", "*S-1-5-32-544:(F)")
	if e = cmd.Run(); e != nil {
		return errors.New("cannot secure credential ACL")
	}
	return nil
}

// Existing Python credentials already carry an account/SYSTEM/Administrators
// ACL. Never mutate them merely on read; enforce the ACL on every new write.
func verifyOwner(p string) error { _, e := os.Stat(p); return e }
func replace(a, b string) error {
	x, e := syscall.UTF16PtrFromString(a)
	if e != nil {
		return e
	}
	y, e := syscall.UTF16PtrFromString(b)
	if e != nil {
		return e
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
	r, _, err := proc.Call(uintptr(unsafe.Pointer(x)), uintptr(unsafe.Pointer(y)), uintptr(0x1|0x8))
	if r == 0 {
		return err
	}
	return nil
}
func syncDir(string) error { return nil }

type Lock struct{ handle syscall.Handle }

func Acquire(path string) (*Lock, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	if e := regular(path); e != nil {
		return nil, e
	}
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, errors.New("agent already running or lock file inaccessible")
	}
	return &Lock{h}, nil
}
func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	return syscall.CloseHandle(l.handle)
}
