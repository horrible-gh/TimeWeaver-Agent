//go:build !windows

package securefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func secureOwner(p string) error { return os.Chmod(p, 0600) }
func verifyOwner(p string) error {
	i, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if i.Mode().Perm()&0077 != 0 {
		return errors.New("credential permissions must be 0600")
	}
	if st, ok := i.Sys().(*syscall.Stat_t); ok && st.Uid != uint32(os.Geteuid()) {
		return errors.New("credential belongs to another OS account")
	}
	return nil
}
func replace(a, b string) error { return os.Rename(a, b) }
func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

type Lock struct{ file *os.File }

func Acquire(path string) (*Lock, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	if e := regular(path); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("agent already running for this credential path")
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}
