package calendar

import (
	"slices"
	"time"

	"github.com/teambition/rrule-go"
)

// satisfiable reports whether any day in one 400-year Gregorian cycle (which repeats exactly)
// passes the rule's day-level filters: BYMONTH, BYMONTHDAY, BYYEARDAY and BYDAY weekdays
// (ordinals ignored, which only widens the set). A false answer means the rule never yields.
// Only rules with BYMONTHDAY or BYYEARDAY are scanned; the others are taken as satisfiable.
func satisfiable(opt *rrule.ROption) bool {
	if len(opt.Bymonthday) == 0 && len(opt.Byyearday) == 0 {
		return true
	}
	var weekdays []int
	for _, w := range opt.Byweekday {
		weekdays = append(weekdays, w.Day())
	}
	d := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for range 146097 {
		if dayPasses(d, opt, weekdays) {
			return true
		}
		d = d.AddDate(0, 0, 1)
	}
	return false
}

func dayPasses(d time.Time, opt *rrule.ROption, weekdays []int) bool {
	if len(opt.Bymonth) > 0 && !slices.Contains(opt.Bymonth, int(d.Month())) {
		return false
	}
	if len(weekdays) > 0 && !slices.Contains(weekdays, (int(d.Weekday())+6)%7) {
		return false
	}
	monthDays := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if len(opt.Bymonthday) > 0 && !matchesDay(opt.Bymonthday, d.Day(), monthDays) {
		return false
	}
	yearDays := 365
	if d.Year()%4 == 0 && (d.Year()%100 != 0 || d.Year()%400 == 0) {
		yearDays = 366
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
