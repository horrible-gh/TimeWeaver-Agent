//go:build !windows

package tasks

import (
	"errors"
	"syscall"
)

func rmdirOnly(path string) error { return syscall.Rmdir(path) }
func directoryNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
