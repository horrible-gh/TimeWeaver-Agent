// Package tasks executes the four existing TimeWeaver task types. It has no
// server or scheduler dependency. Housekeeping intentionally is NOT recursive.
package tasks

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
)

type Executor struct{ Now func() time.Time }

func (e Executor) Run(ctx context.Context, t protocol.Task) (int, *string) {
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	if err := t.Validate(); err != nil {
		return failure(err)
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	base := dateFormat(t.DateFormat, "%Y%m%d")
	src, err := expand(text(t.SourcePath), strftime(now, dateFormat(t.TargetDateFormat, base)))
	if err != nil {
		return failure(err)
	}
	dst, err := expand(text(t.DestinationPath), strftime(now, dateFormat(t.DestinationDateFormat, base)))
	if err != nil {
		return failure(err)
	}
	switch t.Type {
	case "command":
		cmd, err := expand(text(t.Command), strftime(now, base))
		if err != nil {
			return failure(err)
		}
		if err = command(ctx, cmd); err != nil {
			return failure(err)
		}
	case "copy", "archive":
		if _, err = os.Stat(src); os.IsNotExist(err) && !bool(t.ErrorOnMissingSource) {
			return 0, nil
		} else if err != nil {
			return failure(err)
		}
		if t.Type == "copy" {
			err = copyFile(ctx, src, dst)
		} else if text(t.ArchiveType) != "zip" {
			err = errors.New("unsupported archive_type")
		} else {
			err = archive(ctx, src, dst+".zip")
		}
		if err != nil {
			return failure(err)
		}
	case "housekeep":
		if t.HouseKeepDays != nil && *t.HouseKeepDays > 0 && dst != "" {
			if err = Housekeep(ctx, dst, *t.HouseKeepDays, now); err != nil {
				return failure(err)
			}
		}
	}
	return 0, nil
}
func failure(err error) (int, *string) {
	s := strings.ToValidUTF8(err.Error(), "�")
	r := []rune(s)
	if len(r) > 8000 {
		s = string(r[:8000]) + " [truncated]"
	}
	return -1, &s
}
func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	si, err := in.Stat()
	if err != nil {
		return err
	}
	if !si.Mode().IsRegular() {
		return errors.New("copy source must be a regular file")
	}
	if di, e := os.Stat(dst); e == nil && os.SameFile(si, di) {
		return errors.New("copy source and destination are the same file")
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0666)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, contextReader{ctx, in})
	ce := out.Close()
	if err != nil {
		return err
	}
	return ce
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func archive(ctx context.Context, src, dst string) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !si.IsDir() {
		return errors.New("archive source must be a directory")
	}
	sa, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	da, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(sa, da)
	if err != nil {
		return err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("archive output must not be inside its source")
	}
	// Match Python: the archive destination's parent must already exist.
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(walkErr, fs.ErrPermission) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("archive contains a non-regular file")
		}
		f, err := os.Open(path)
		if errors.Is(err, fs.ErrPermission) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		name, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(name)
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, contextReader{ctx, f})
		return err
	})
	ze := zw.Close()
	oe := out.Close()
	if err != nil {
		return err
	}
	if ze != nil {
		return ze
	}
	return oe
}

// Housekeep uses file modification time and removes direct children only.
// Old real directories are an error, as in Python os.remove; no RemoveAll,
// recursion, active-file detection or new vacuum policy is implied.
func Housekeep(ctx context.Context, path string, days int, now time.Time) error {
	if days <= 0 {
		return nil
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	cutoff := float64(now.Unix()) + float64(now.Nanosecond())/1e9 - float64(days)*86400
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(path, item.Name())
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if float64(info.ModTime().Unix())+float64(info.ModTime().Nanosecond())/1e9 < cutoff {
			if item.IsDir() {
				return fmt.Errorf("housekeep does not remove directories: %s", item.Name())
			}
			if err = os.Remove(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// expand implements the public {date} placeholder and Python's escaped braces.
func expand(s, date string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '{':
			if i+1 < len(s) && s[i+1] == '{' {
				out.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(s[i+1:], '}')
			if end < 0 {
				return "", errors.New("unclosed template brace")
			}
			key := s[i+1 : i+1+end]
			if key != "date" && key != "date:s" {
				return "", errors.New("unsupported template field (use {date}; escape literal braces)")
			}
			out.WriteString(date)
			i += end + 2
		case '}':
			if i+1 < len(s) && s[i+1] == '}' {
				out.WriteByte('}')
				i += 2
			} else {
				return "", errors.New("unmatched template brace")
			}
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String(), nil
}
func dateFormat(p *string, fallback string) string {
	s := text(p)
	if s == "" || strings.EqualFold(strings.TrimSpace(s), "none") {
		return fallback
	}
	directives := map[byte]bool{}
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		i++
		if i == len(s) {
			return fallback
		}
		// The old Python task uses a naive datetime; %z/%Z cannot round-trip
		// its validation date. %g and platform-specific directives also fail.
		if !strings.ContainsRune("aAbBcdHIjmMpSUwWxXyYfVGu%", rune(s[i])) {
			return fallback
		}
		directives[s[i]] = true
	}
	if directives['G'] || directives['V'] {
		weekday := directives['a'] || directives['A'] || directives['w'] || directives['u']
		if !directives['G'] || !directives['V'] || !weekday || directives['j'] {
			return fallback
		}
	} else if !directives['Y'] && !directives['y'] && !directives['c'] && !directives['x'] {
		return fallback
	}
	return s
}

func strftime(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 == len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		var s string
		switch format[i] {
		case '%':
			s = "%"
		case 'Y':
			s = fmt.Sprintf("%04d", t.Year())
		case 'y':
			s = t.Format("06")
		case 'm':
			s = t.Format("01")
		case 'd':
			s = t.Format("02")
		case 'H':
			s = t.Format("15")
		case 'I':
			s = t.Format("03")
		case 'M':
			s = t.Format("04")
		case 'S':
			s = t.Format("05")
		case 'f':
			s = fmt.Sprintf("%06d", t.Nanosecond()/1000)
		case 'a':
			s = t.Format("Mon")
		case 'A':
			s = t.Format("Monday")
		case 'b':
			s = t.Format("Jan")
		case 'B':
			s = t.Format("January")
		case 'p':
			s = t.Format("PM")
		case 'j':
			s = fmt.Sprintf("%03d", t.YearDay())
		case 'w':
			s = fmt.Sprint(int(t.Weekday()))
		case 'u':
			s = fmt.Sprint((int(t.Weekday())+6)%7 + 1)
		case 'z':
			s = t.Format("-0700")
		case 'Z':
			s = t.Format("MST")
		case 'G', 'g', 'V':
			y, w := t.ISOWeek()
			if format[i] == 'V' {
				s = fmt.Sprintf("%02d", w)
			} else if format[i] == 'g' {
				s = fmt.Sprintf("%02d", y%100)
			} else {
				s = fmt.Sprintf("%04d", y)
			}
		case 'U', 'W':
			wd := int(t.Weekday())
			if format[i] == 'W' {
				wd = (wd + 6) % 7
			}
			s = fmt.Sprintf("%02d", (t.YearDay()-1+7-wd)/7)
		case 'c':
			s = t.Format("Mon Jan _2 15:04:05 2006")
		case 'x':
			s = t.Format("01/02/06")
		case 'X':
			s = t.Format("15:04:05")
		}
		b.WriteString(s)
	}
	return b.String()
}
