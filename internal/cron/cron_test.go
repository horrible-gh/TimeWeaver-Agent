package cron

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMondayZeroAndDayIntersection(t *testing.T) {
	f := EverySecond()
	f.Year = "2026"
	f.Month = "jan"
	f.Day = "5"
	f.DayOfWeek = "0"
	f.Hour = "9"
	f.Minute = "0"
	f.Second = "3"
	s, e := Compile(f)
	if e != nil {
		t.Fatal(e)
	}
	yes := time.Date(2026, 1, 5, 9, 0, 3, 0, time.UTC)
	if !s.Match(yes) {
		t.Fatal("Monday 0 not matched")
	}
	for _, d := range []time.Time{yes.Add(time.Second), yes.AddDate(0, 0, 7), yes.AddDate(0, 0, -1), yes.AddDate(1, 0, 0)} {
		if s.Match(d) {
			t.Fatal("fields must ALL match", d)
		}
	}
}
func TestRangesNamesAndSteps(t *testing.T) {
	f := EverySecond()
	f.Month = "jan,mar-may"
	f.DayOfWeek = "mon-fri"
	f.Minute = "5-25/5"
	s, e := Compile(f)
	if e != nil {
		t.Fatal(e)
	}
	base := time.Date(2026, 3, 2, 12, 10, 0, 0, time.UTC)
	if !s.Match(base) || s.Match(base.Add(time.Minute)) {
		t.Fatal("step mismatch")
	}
}
func TestRejectUnknownCronSyntax(t *testing.T) {
	for _, expr := range []Expr{"", " 1", "?", "1/0", "-1", "60", "0-100", "0,", "1-2-3", "bogus", "1#2"} {
		t.Run(string(expr), func(t *testing.T) {
			f := EverySecond()
			f.Minute = expr
			if _, e := Compile(f); e == nil {
				t.Fatal("accepted", expr)
			}
		})
	}
	f := EverySecond()
	f.DayOfWeek = "7"
	if _, e := Compile(f); e == nil {
		t.Fatal("Sunday is 6, not 7")
	}
}
func TestExpressionsAcceptIntegersRejectNull(t *testing.T) {
	for _, input := range []string{`"*/5"`, `0`, `"mon-fri"`} {
		var e Expr
		if err := json.Unmarshal([]byte(input), &e); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []string{`null`, `true`, `[]`, `1.5`} {
		var e Expr
		if err := json.Unmarshal([]byte(input), &e); err == nil {
			t.Fatal(input)
		}
	}
}
func TestCoalescingAndMisfireGrace(t *testing.T) {
	s, _ := Compile(EverySecond())
	now := time.Date(2026, 1, 5, 9, 2, 0, 0, time.UTC)
	got, ok := s.Latest(now.Add(-2*time.Hour), now, time.UTC)
	if !ok || !got.Equal(now) {
		t.Fatal(got, ok)
	}
	f := EverySecond()
	f.Minute = "0"
	f.Second = "0"
	s, _ = Compile(f)
	if _, ok = s.Latest(now.Add(-2*time.Hour), now, time.UTC); ok {
		t.Fatal("expired misfire fired")
	}
}
func TestDSTFoldUsesActualInstants(t *testing.T) {
	loc, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Skip(e)
	}
	f := EverySecond()
	f.Hour = "1"
	f.Minute = "30"
	f.Second = "0"
	s, _ := Compile(f)
	for _, hour := range []int{5, 6} {
		now := time.Date(2026, 11, 1, hour, 30, 0, 0, time.UTC)
		if _, ok := s.Latest(now.Add(-time.Second), now, loc); !ok {
			t.Fatal("missing DST occurrence", now)
		}
	}
}
func TestOversizedStepRejected(t *testing.T) {
	f := EverySecond()
	f.Minute = "*/60"
	if _, err := Compile(f); err == nil {
		t.Fatal("step exceeds field range")
	}
}
