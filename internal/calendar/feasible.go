package calendar

import (
	"slices"
	"time"

	"github.com/teambition/rrule-go"
)

// satisfiable reports whether any date passes the rule's BYMONTH, BYMONTHDAY and BYYEARDAY
// filters, over a non-leap and a leap year template. BYDAY weekdays are ignored: every calendar
// date falls on every weekday within the 400-year Gregorian cycle. A false answer means the rule
// never yields. Only rules with BYMONTHDAY or BYYEARDAY are checked; the rest are taken as
// satisfiable.
func satisfiable(opt *rrule.ROption) bool {
	if len(opt.Bymonthday) == 0 && len(opt.Byyearday) == 0 {
		return true
	}
	for _, year := range []int{2001, 2000} {
		yearDays := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC).YearDay()
		for d := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC); d.Year() == year; d = d.AddDate(0, 0, 1) {
			if dayPasses(d, opt, yearDays) {
				return true
			}
		}
	}
	return false
}

func dayPasses(d time.Time, opt *rrule.ROption, yearDays int) bool {
	if len(opt.Bymonth) > 0 && !slices.Contains(opt.Bymonth, int(d.Month())) {
		return false
	}
	monthDays := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if len(opt.Bymonthday) > 0 && !matchesDay(opt.Bymonthday, d.Day(), monthDays) {
		return false
	}
	return len(opt.Byyearday) == 0 || matchesDay(opt.Byyearday, d.YearDay(), yearDays)
}

// matchesDay tests a 1-based position n from the start (n > 0) or end (n < 0) of a period.
func matchesDay(set []int, pos, length int) bool {
	return slices.Contains(set, pos) || slices.Contains(set, pos-length-1)
}

// ordinalsInRange refuses BYDAY ordinals no period can contain (rrule-go would scan to year 9999).
func ordinalsInRange(opt *rrule.ROption) bool {
	limit := 53
	if opt.Freq == rrule.MONTHLY || len(opt.Bymonth) > 0 {
		limit = 5
	}
	for _, w := range opt.Byweekday {
		if n := w.N(); n > limit || n < -limit {
			return false
		}
	}
	return true
}

// probeYears is how many years before rrule-go's MAXYEAR (9999, util.go) a probe starts. Coarse
// periods get the full 400-year cycle; DAILY and HOURLY sets do not depend on the date beyond the
// day filters, and a longer scan costs too much (HOURLY: ~0.27 s per 400 years).
func probeYears(f rrule.Frequency) int {
	switch f {
	case rrule.HOURLY:
		return 8
	case rrule.DAILY:
		return 40
	}
	return 400
}

// yields probes a BYSETPOS or BYWEEKNO rule, or an ordinal BYDAY with a day filter, with
// rrule-go itself: it must produce an occurrence before rrule-go stops at year 9999.
func yields(opt *rrule.ROption, start time.Time) bool {
	if !clockReachable(opt, start) {
		return false
	}
	if len(opt.Bysetpos) == 0 && len(opt.Byweekno) == 0 && !ordinalWithDayFilter(opt) {
		return true
	}
	probe := *opt
	probe.Count, probe.Until = 0, time.Time{}
	h, m, s := start.Clock()
	probe.Dtstart = time.Date(10000-probeYears(opt.Freq), 1, 1, h, m, s, 0, start.Location())
	r, err := rrule.NewRRule(probe)
	if err != nil {
		return false
	}
	_, ok := r.Iterator()()
	return ok
}

// clockReachable reports whether an HOURLY, MINUTELY or SECONDLY rule's steps from DTSTART can
// land on its BYHOUR/BYMINUTE/BYSECOND clock: rrule-go loops without end until one does (e.g.
// FREQ=HOURLY;INTERVAL=2;BYHOUR=1 from an even hour). Steps reach exactly the seconds of the day
// congruent to DTSTART's modulo gcd(step, 86400).
func clockReachable(opt *rrule.ROption, start time.Time) bool {
	const day = 86400
	unit := map[rrule.Frequency]int{rrule.HOURLY: 3600, rrule.MINUTELY: 60, rrule.SECONDLY: 1}[opt.Freq]
	if unit == 0 {
		return true
	}
	g := gcd(max(opt.Interval, 1)%day*unit%day, day)
	h0, m0, s0 := start.Clock()
	hours, minutes, seconds := orAll(opt.Byhour, 24), []int{m0}, []int{s0}
	if opt.Freq != rrule.HOURLY {
		minutes = orAll(opt.Byminute, 60)
	}
	if opt.Freq == rrule.SECONDLY {
		seconds = orAll(opt.Bysecond, 60)
	}
	for _, h := range hours {
		for _, m := range minutes {
			for _, s := range seconds {
				if ((h-h0)*3600+(m-m0)*60+s-s0)%g == 0 {
					return true
				}
			}
		}
	}
	return false
}

// orAll is set, or every value below n when set is empty.
func orAll(set []int, n int) []int {
	if len(set) > 0 {
		return set
	}
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	return all
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// ordinalWithDayFilter: satisfiable ignores BYDAY ordinals, so "1MO" with BYMONTHDAY=15 passes it.
func ordinalWithDayFilter(opt *rrule.ROption) bool {
	if len(opt.Bymonthday) == 0 && len(opt.Byyearday) == 0 {
		return false
	}
	return slices.ContainsFunc(opt.Byweekday, func(w rrule.Weekday) bool { return w.N() != 0 })
}
