//go:build !windows

package tasks

import "io/fs"

func housekeepLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }
