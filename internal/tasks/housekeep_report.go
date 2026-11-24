package tasks

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
)

// The successful result message prefix is shared with the server's dashboard
// classification. Result code zero retains compatibility with older servers
// and avoids retries, error-stop transitions and failed manual runs.
const housekeepWarningPrefix = "[housekeep-warning] "

type housekeepReport struct {
	count int
	path  string
	cause string
}

func (r *housekeepReport) skip(path, cause string) {
	r.count++
	if r.count == 1 {
		r.path, r.cause = path, cause
	}
}

func (r *housekeepReport) skipDenied(root, path string, cause error) bool {
	if path == root || !errors.Is(cause, fs.ErrPermission) {
		return false
	}
	r.skip(path, "permission denied")
	return true
}

func (r *housekeepReport) message() *string {
	if r.count == 0 {
		return nil
	}
	slog.Warn("housekeep skipped paths", "count", r.count, "example_path", r.path, "reason", r.cause)
	return resultMessage(fmt.Sprintf("%sSkipped %d path(s): %s (%s)", housekeepWarningPrefix, r.count, r.path, r.cause))
}
