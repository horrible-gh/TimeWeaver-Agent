package tasks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HousekeepRecursive removes expired regular files, never directories or links.
// The configured root must be a real directory controlled by the agent account.
// Like legacy housekeeping this is not in-use-file protection or a filesystem
// sandbox against an attacker concurrently replacing parent directories.
func HousekeepRecursive(ctx context.Context, path string, days int, now time.Time) error {
	var report housekeepReport
	err := housekeepRecursive(ctx, path, days, now, &report, false)
	report.message()
	return err
}

func housekeepRecursive(ctx context.Context, path string, days int, now time.Time, report *housekeepReport, deleteEmpty bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if days <= 0 && !deleteEmpty {
		return nil
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if housekeepLink(info) || !info.IsDir() {
		return errors.New("recursive housekeep root must be a directory, not a link or reparse point")
	}
	// Do not multiply a duration: large retention values must not overflow.
	cutoff := float64(now.Unix()) + float64(now.Nanosecond())/1e9 - float64(days)*86400
	expired := func(info fs.FileInfo) bool {
		modified := info.ModTime()
		return float64(modified.Unix())+float64(modified.Nanosecond())/1e9 < cutoff
	}
	// WalkDir visits directories before their children. Flush a depth-bounded
	// stack when leaving each subtree so newly empty parents are removed last.
	type candidate struct {
		path string
		info fs.FileInfo
	}
	var pending []candidate
	flush := func(next string) error {
		for len(pending) > 0 {
			last := pending[len(pending)-1]
			if next == last.path || strings.HasPrefix(next, last.path+string(os.PathSeparator)) {
				break
			}
			pending = pending[:len(pending)-1]
			if err := removeEmptyDirectory(ctx, root, last.path, last.info, report); err != nil {
				return err
			}
		}
		return nil
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := flush(path); err != nil {
			return err
		}
		if walkErr != nil {
			// A directory whose enumeration failed is never a deletion candidate.
			if len(pending) > 0 && pending[len(pending)-1].path == path {
				pending = pending[:len(pending)-1]
			}
			if path != root && errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if report.skipDenied(root, path, walkErr) {
				return nil
			}
			return walkErr
		}
		// Info uses lstat semantics. In particular, Windows junctions must be
		// checked before IsDir so they cannot extend cleanup outside the root.
		info, err := entry.Info()
		if err != nil {
			if path != root && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if report.skipDenied(root, path, err) {
				return nil
			}
			return err
		}
		if housekeepLink(info) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			if deleteEmpty && path != root {
				pending = append(pending, candidate{path, info})
			}
			return nil
		}
		if days <= 0 || !info.Mode().IsRegular() || !expired(info) {
			return nil
		}
		// Recheck the identity and mtime: do not remove a file replaced or updated
		// since enumeration. A disappearing file is not a failure of cleanup.
		current, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			if report.skipDenied(root, path, err) {
				return nil
			}
			return err
		}
		if housekeepLink(current) || !current.Mode().IsRegular() || !os.SameFile(info, current) || !expired(current) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if report.skipDenied(root, path, err) {
				return nil
			}
			return fmt.Errorf("housekeep remove %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(""); err != nil {
		return err
	}
	return ctx.Err()
}
