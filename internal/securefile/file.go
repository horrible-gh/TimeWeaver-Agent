// Package securefile provides crash-resistant private state and instance locks.
package securefile

import (
	"errors"
	"os"
	"path/filepath"
)

func regular(path string) error {
	i, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return errors.New("state path must be a regular file, not a symlink")
	}
	return nil
}
func ReadPrivate(path string) ([]byte, error) {
	if e := regular(path); e != nil {
		return nil, e
	}
	if e := verifyOwner(path); e != nil {
		return nil, e
	}
	return os.ReadFile(path)
}
func Write(path string, b []byte, private bool) error {
	if e := regular(path); e != nil {
		return e
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".timeweaver-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if private {
		if e = secureOwner(tmp); e != nil {
			f.Close()
			return e
		}
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = replace(tmp, path); e != nil {
		return e
	}
	return syncDir(dir)
}
