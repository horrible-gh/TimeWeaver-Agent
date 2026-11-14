// Package cron implements the effective APScheduler 3 cron contract used by
// TimeWeaver: seven independent fields, Monday=0, and AND day semantics.
// Quartz syntax is rejected, never silently reinterpreted.
package cron

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Expr string

func (e *Expr) UnmarshalJSON(b []byte) error {
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		n, err := strconv.Atoi(string(b))
		if err != nil {
			return fmt.Errorf("cron field must be a string or integer")
		}
		s = strconv.Itoa(n)
	}
	if s == "" || strings.TrimSpace(s) != s {
		return fmt.Errorf("empty or padded cron expression")
	}
	*e = Expr(s)
	return nil
}

type Fields struct {
	Year      Expr `json:"year"`
	Month     Expr `json:"month"`
	DayOfWeek Expr `json:"day_of_week"`
	Day       Expr `json:"day"`
	Hour      Expr `json:"hour"`
	Minute    Expr `json:"minute"`
	Second    Expr `json:"second"`
}

func EverySecond() Fields { return Fields{"*", "*", "*", "*", "*", "*", "*"} }

type Schedule struct{ values [7][]span }

type span struct{ low, high, step int }

var months = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var weekdays = map[string]int{"mon": 0, "tue": 1, "wed": 2, "thu": 3, "fri": 4, "sat": 5, "sun": 6}

func Compile(f Fields) (*Schedule, error) {
	s := &Schedule{}
	exprs := []Expr{f.Year, f.Month, f.DayOfWeek, f.Day, f.Hour, f.Minute, f.Second}
	bounds := [][2]int{{1970, 9999}, {1, 12}, {0, 6}, {1, 31}, {0, 23}, {0, 59}, {0, 59}}
	names := []string{"year", "month", "day_of_week", "day", "hour", "minute", "second"}
	for i, e := range exprs {
		var aliases map[string]int
		if i == 1 {
			aliases = months
		}
		if i == 2 {
			aliases = weekdays
		}
		v, err := parse(string(e), bounds[i][0], bounds[i][1], aliases)
		if err != nil {
			return nil, fmt.Errorf("cron.%s: %w", names[i], err)
		}
		s.values[i] = v
	}
	return s, nil
}
func parse(expr string, min, max int, aliases map[string]int) ([]span, error) {
	if expr == "" || len(expr) > 1024 || strings.TrimSpace(expr) != expr {
		return nil, fmt.Errorf("invalid expression")
	}
	var out []span
	number := func(s string) (int, error) {
		if n, ok := aliases[strings.ToLower(s)]; ok {
			return n, nil
		}
		return strconv.Atoi(s)
	}
	for _, part := range strings.Split(expr, ",") {
		bits := strings.Split(part, "/")
		if len(bits) > 2 || bits[0] == "" {
			return nil, fmt.Errorf("invalid range/step")
		}
		step := 1
		var err error
		if len(bits) == 2 {
			step, err = strconv.Atoi(bits[1])
			if err != nil || step < 1 {
				return nil, fmt.Errorf("invalid step")
			}
		}
		lo, hi := min, max
		if bits[0] != "*" {
			r := strings.Split(bits[0], "-")
			if len(r) > 2 {
				return nil, fmt.Errorf("invalid range")
			}
			lo, err = number(r[0])
			if err != nil {
				return nil, fmt.Errorf("unsupported expression")
			}
			hi = lo
			if len(r) == 2 {
				hi, err = number(r[1])
				if err != nil {
					return nil, fmt.Errorf("invalid range")
				}
			} else if len(bits) == 2 {
				hi = max
			}
		}
		if lo < min || hi > max || lo > hi || (len(bits) == 2 && step > hi-lo) {
			return nil, fmt.Errorf("range/step outside bounds %d..%d", min, max)
		}
		out = append(out, span{lo, hi, step})
	}
	return out, nil
}
func (s *Schedule) Match(t time.Time) bool {
	if s == nil {
		return false
	}
	vals := [7]int{t.Year(), int(t.Month()), (int(t.Weekday()) + 6) % 7, t.Day(), t.Hour(), t.Minute(), t.Second()}
	for i, v := range vals {
		matched := false
		for _, r := range s.values[i] {
			if v >= r.low && v <= r.high && (v-r.low)%r.step == 0 {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// Latest returns the last matching instant in (after, now], coalescing missed
// fires, with a hard 60-second grace bound like the Python adapter. Absolute
// instants are walked so DST folds and gaps follow the selected local zone.
func (s *Schedule) Latest(after, now time.Time, loc *time.Location) (time.Time, bool) {
	now = now.Truncate(time.Second)
	first := after.Truncate(time.Second).Add(time.Second)
	floor := now.Add(-60 * time.Second)
	if first.Before(floor) {
		first = floor
	}
	for t := now; !t.Before(first); t = t.Add(-time.Second) {
		if s.Match(t.In(loc)) {
			return t, true
		}
	}
	return time.Time{}, false
}
