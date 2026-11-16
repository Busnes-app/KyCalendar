package tasks

import (
	"slices"
	"time"

	"github.com/emersion/go-webdav/caldav"
)

// Span is a stretch of time.
type Span struct{ Start, End time.Time }

// Day is a day's busy blocks, merged, and the free gaps between from and to.
type Day struct{ Busy, Free []Span }

// Gaps merges busy periods clipped to [from, to) and returns them with the gaps between.
func Gaps(from, to time.Time, busy []caldav.FreeBusyPeriod) Day {
	var spans []Span
	for _, p := range busy {
		s, e := maxTime(p.Start, from), minTime(p.End, to)
		if e.After(s) {
			spans = append(spans, Span{s, e})
		}
	}
	slices.SortFunc(spans, func(a, b Span) int { return a.Start.Compare(b.Start) })
	var d Day
	for _, s := range spans {
		if n := len(d.Busy); n > 0 && !s.Start.After(d.Busy[n-1].End) {
			d.Busy[n-1].End = maxTime(d.Busy[n-1].End, s.End)
			continue
		}
		d.Busy = append(d.Busy, s)
	}
	at := from
	for _, b := range d.Busy {
		if b.Start.After(at) {
			d.Free = append(d.Free, Span{at, b.Start})
		}
		at = maxTime(at, b.End)
	}
	if to.After(at) {
		d.Free = append(d.Free, Span{at, to})
	}
	return d
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
