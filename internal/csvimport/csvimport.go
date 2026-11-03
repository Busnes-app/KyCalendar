// Package csvimport reads the calendar CSV files Outlook exports and Google Calendar imports into
// plain event rows. It is pure: no store, no HTTP. The API validates and writes the rows.
package csvimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// MaxRows bounds one file, as feed.MaxObjects bounds one subscription.
const MaxRows = 5000

// ErrNotCalendar is a file without the Subject and Start Date columns, or with no data rows.
var ErrNotCalendar = errors.New("not a calendar CSV")

// ErrTooManyRows is a file over MaxRows.
var ErrTooManyRows = errors.New("too many rows")

// Row is one event. Timed rows carry instants in the import zone; all-day rows carry dates at
// UTC midnight with an exclusive End.
type Row struct {
	Line                         int // the spreadsheet row number; the header is row 1
	Title, Location, Description string
	Start, End                   time.Time
	AllDay                       bool
}

// UID is stable for the same event imported twice, so a second import skips it.
func (r Row) UID() string {
	h := sha256.Sum256([]byte(strings.Join([]string{r.Title, r.Start.UTC().Format(time.RFC3339), r.End.UTC().Format(time.RFC3339), strconv.FormatBool(r.AllDay)}, "\x00")))
	return "csv-" + hex.EncodeToString(h[:16])
}

// Skip is a row that was not read, and why.
type Skip struct {
	Line   int    `json:"row"`
	Reason string `json:"reason"`
}

// Parse reads data in zone. dayFirst picks DD/MM over MM/DD only when no date in the file
// settles the order itself.
func Parse(data []byte, zone *time.Location, dayFirst bool) ([]Row, []Skip, error) {
	text, err := decode(data)
	if err != nil {
		return nil, nil, err
	}
	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	// Read record by record so a file of many tiny rows stops at the cap instead of being held whole.
	var records [][]string
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrNotCalendar, err)
		}
		if len(records) > MaxRows { // the header plus MaxRows data rows are allowed
			return nil, nil, ErrTooManyRows
		}
		records = append(records, rec)
	}
	if len(records) < 2 {
		return nil, nil, ErrNotCalendar
	}
	col := map[string]int{}
	for i, h := range records[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := col["subject"]; !ok {
		return nil, nil, ErrNotCalendar
	}
	if _, ok := col["start date"]; !ok {
		return nil, nil, ErrNotCalendar
	}
	_, reminder := col["reminder on/off"]
	_, showAs := col["show time as"]
	// Outlook writes an all-day event's End Date as the day after; Google's format names the last day.
	outlook := reminder || showAs
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	order := dateOrder(records[1:], get, dayFirst)

	var rows []Row
	var skips []Skip
	for i, rec := range records[1:] {
		line := i + 2
		if blank(rec) {
			continue
		}
		row, reason := readRow(rec, get, zone, order, outlook)
		if reason != "" {
			skips = append(skips, Skip{Line: line, Reason: reason})
			continue
		}
		row.Line = line
		rows = append(rows, row)
	}
	return rows, skips, nil
}

// decode returns data as UTF-8: a UTF-16 file by its byte order mark, a UTF-8 one as is, and
// anything else as Windows-1252, which classic Outlook writes.
func decode(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		out, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder(), data)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNotCalendar, err)
		}
		return string(out), nil
	case utf8.Valid(data):
		return strings.TrimPrefix(string(data), "\ufeff"), nil
	default:
		out, _, err := transform.Bytes(charmap.Windows1252.NewDecoder(), data)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNotCalendar, err)
		}
		return string(out), nil
	}
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

type order int

const (
	monthFirst order = iota
	dayFirstOrder
)

// dateOrder reads the order from the file: a first part over 12 or dotted dates mean day first,
// a second part over 12 means month first. Year-first dates settle nothing. Only an undecided
// file falls back to the hint.
func dateOrder(records [][]string, get func([]string, string) string, hint bool) order {
	for _, rec := range records {
		for _, name := range []string{"start date", "end date"} {
			v := get(rec, name)
			parts := dateParts(v)
			if len(parts) != 3 || len(parts[0]) == 4 {
				continue
			}
			if strings.Contains(v, ".") {
				return dayFirstOrder
			}
			a, _ := strconv.Atoi(parts[0])
			b, _ := strconv.Atoi(parts[1])
			if a > 12 && a <= 31 {
				return dayFirstOrder
			}
			if b > 12 && b <= 31 {
				return monthFirst
			}
		}
	}
	if hint {
		return dayFirstOrder
	}
	return monthFirst
}

func dateParts(v string) []string {
	return strings.FieldsFunc(v, func(c rune) bool { return c == '/' || c == '.' || c == '-' })
}

func readRow(rec []string, get func([]string, string) string, zone *time.Location, o order, outlook bool) (Row, string) {
	r := Row{Title: get(rec, "subject"), Location: get(rec, "location"), Description: newlines(get(rec, "description"))}
	startDay, ok := parseDate(get(rec, "start date"), o)
	if !ok {
		return r, "Start Date is missing or not a date"
	}
	endDay := startDay
	if v := get(rec, "end date"); v != "" {
		if endDay, ok = parseDate(v, o); !ok {
			return r, "End Date is not a date"
		}
	}
	startTime, endTime := get(rec, "start time"), get(rec, "end time")
	flag := strings.ToLower(get(rec, "all day event"))
	r.AllDay = flag == "true" || flag == "yes" || flag == "1" || (startTime == "" && endTime == "")
	if r.AllDay {
		if endDay.Before(startDay) {
			return r, "it ends before it starts"
		}
		if !outlook || !endDay.After(startDay) {
			endDay = endDay.AddDate(0, 0, 1)
		}
		r.Start, r.End = startDay, endDay
		return r, ""
	}
	sh, sm, ss, ok := parseClock(startTime)
	if !ok {
		return r, "Start Time is missing or not a time"
	}
	r.Start = time.Date(startDay.Year(), startDay.Month(), startDay.Day(), sh, sm, ss, 0, zone)
	if endTime == "" {
		r.End = r.Start.Add(time.Hour)
	} else {
		eh, em, es, ok := parseClock(endTime)
		if !ok {
			return r, "End Time is not a time"
		}
		r.End = time.Date(endDay.Year(), endDay.Month(), endDay.Day(), eh, em, es, 0, zone)
	}
	if r.End.Before(r.Start) {
		return r, "it ends before it starts"
	}
	return r, ""
}

func newlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// parseDate reads three numbers split by "/", "." or "-": year first when the first has four
// digits (2026-03-02, 2026/03/02), else in order o, with a two-digit year meaning 20xx.
func parseDate(v string, o order) (time.Time, bool) {
	parts := dateParts(v)
	if len(parts) != 3 {
		return time.Time{}, false
	}
	n := [3]int{}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || len(p) > 4 {
			return time.Time{}, false
		}
		n[i] = x
	}
	var year, month, day int
	switch {
	case len(parts[0]) == 4:
		year, month, day = n[0], n[1], n[2]
	case o == dayFirstOrder:
		day, month, year = n[0], n[1], n[2]
	default:
		month, day, year = n[0], n[1], n[2]
	}
	if len(parts[0]) != 4 && len(parts[2]) == 2 {
		year += 2000
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || t.Month() != time.Month(month) || t.Day() != day { // 31/02 must not roll into March
		return time.Time{}, false
	}
	return t, true
}

var clockLayouts = []string{"15:04:05", "15:04", "3:04:05 PM", "3:04 PM", "3:04:05PM", "3:04PM", "3 PM", "3PM"}

func parseClock(v string) (h, m, s int, ok bool) {
	v = strings.NewReplacer("A.M.", "AM", "P.M.", "PM").Replace(strings.ToUpper(v))
	for _, layout := range clockLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Hour(), t.Minute(), t.Second(), true
		}
	}
	return 0, 0, 0, false
}
