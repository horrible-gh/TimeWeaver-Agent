package tasks

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
)

// Only remove the original, empty real directory. Directory-only OS removal
// must fail if a file replaces the directory or new entries appear after the
// emptiness check. Never use RemoveAll or follow links/reparse points.
func removeEmptyDirectory(ctx context.Context, root, path string, original fs.FileInfo, report *housekeepReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == root || !original.IsDir() || housekeepLink(original) {
		return nil
	}
	handleError := func(err error) error {
		if err == nil || errors.Is(err, fs.ErrNotExist) || report.skipDenied(root, path, err) {
			return nil
		}
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return handleError(err)
	}
	if !current.IsDir() || housekeepLink(current) || !os.SameFile(original, current) {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return handleError(err)
	}
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(original, opened) {
		dir.Close()
		return handleError(err)
	}
	_, err = dir.Readdirnames(1)
	closeErr := dir.Close()
	if err == nil {
		return closeErr
	}
	if !errors.Is(err, io.EOF) {
		return handleError(err)
	}
	if closeErr != nil {
		return handleError(closeErr)
	}
	current, err = os.Lstat(path)
	if err != nil {
		return handleError(err)
	}
	if !current.IsDir() || housekeepLink(current) || !os.SameFile(original, current) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = rmdirOnly(path)
	if directoryNotEmpty(err) {
		return nil
	}
	return handleError(err)
}
