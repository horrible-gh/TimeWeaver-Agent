//go:build windows

package tasks

import (
	"errors"
	"syscall"
)

func rmdirOnly(path string) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.RemoveDirectory(name)
}
func directoryNotEmpty(err error) bool {
	return errors.Is(err, syscall.Errno(145)) // ERROR_DIR_NOT_EMPTY
}
