package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/csvimport"
)

// maxReportedSkips bounds the skipped rows a response lists; skipped_count has them all.
const maxReportedSkips = 100

// importWriteBudget is how long the connection may stay open for the report: csvimport.MaxRows
// writes take seconds, and the listener's WriteTimeout is sized for JSON replies.
const importWriteBudget = 5 * time.Minute

type importView struct {
	Imported     int              `json:"imported"`
	Duplicates   int              `json:"duplicates"`
	Skipped      []csvimport.Skip `json:"skipped"`
	SkippedCount int              `json:"skipped_count"`
	// Stopped is set when the import ended early, for example on a full storage quota; the rows
	// before it stay imported.
	Stopped string `json:"stopped,omitempty"`
}

// handleImportCSV adds the events of an Outlook or Google calendar CSV, the request body, to a
// calendar. Rows already imported (same title and times, so the same UID) are skipped.
func (s *Server) handleImportCSV(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if !access.CanEditEvents(c, role) {
		s.writeAPIError(w, errReadOnlyCalendar)
		return
	}
	if !c.Holds(ical.CompEvent) {
		s.writeAPIError(w, &apiError{status: http.StatusUnprocessableEntity, msg: "This calendar holds tasks, not events", code: "tasks_only"})
		return
	}
	q := r.URL.Query()
	zone, ok := zoneNamed(q.Get("tz"))
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	dayFirst := false
	switch q.Get("date_order") {
	case "", "mdy":
	case "dmy":
		dayFirst = true
	default:
		s.writeError(w, http.StatusBadRequest, "date_order must be mdy or dmy")
		return
	}
	data, err := io.ReadAll(r.Body)
	if mb := (*http.MaxBytesError)(nil); errors.As(err, &mb) {
		s.writeAPIError(w, &apiError{status: http.StatusRequestEntityTooLarge, msg: "The file is too large; the limit is 1 MiB", code: "too_large"})
		return
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to read the file")
		return
	}
	rows, skips, err := csvimport.Parse(data, zone, dayFirst)
	switch {
	case errors.Is(err, csvimport.ErrTooManyRows):
		s.writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("The file has more than %d rows", csvimport.MaxRows))
		return
	case err != nil:
		s.writeError(w, http.StatusUnprocessableEntity, "This is not an Outlook or Google Calendar CSV: it needs Subject and Start Date columns and at least one event")
		return
	}

	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(importWriteBudget))
	// Detached: a dropped connection must not stop an import halfway without a report or audit row.
	ctx := context.WithoutCancel(r.Context())
	user := sessionUser(r.Context())
	now := time.Now()
	v := importView{}
	for _, row := range rows {
		in := calendar.EventInput{Title: row.Title, Location: row.Location, Description: row.Description,
			Start: row.Start, End: row.End, AllDay: row.AllDay}
		if !row.AllDay {
			in.Zone = zone
		}
		if err := checkText(row.Title, row.Location, row.Description); err != nil {
			skips = append(skips, csvimport.Skip{Line: row.Line, Reason: "its text is too long or has control characters"})
			continue
		}
		if !yearsInRange(row.Start, row.End) {
			skips = append(skips, csvimport.Skip{Line: row.Line, Reason: "years must be 1900 to 9000"})
			continue
		}
		uid := row.UID()
		_, apiErr := s.saveEvent(ctx, user, c, uid+".ics", calendar.NewEvent(uid, in, now), "", true)
		switch {
		case apiErr == nil:
			v.Imported++
			continue
		case apiErr == errEventChanged, apiErr.status == http.StatusConflict: // already imported
			v.Duplicates++
			continue
		case apiErr.status == http.StatusRequestEntityTooLarge, apiErr.status == http.StatusUnprocessableEntity:
			skips = append(skips, csvimport.Skip{Line: row.Line, Reason: apiErr.msg})
			continue
		}
		v.Stopped = fmt.Sprintf("Stopped at row %d: %s", row.Line, apiErr.msg)
		break
	}
	slices.SortStableFunc(skips, func(a, b csvimport.Skip) int { return a.Line - b.Line })
	v.SkippedCount = len(skips)
	v.Skipped = skips[:min(len(skips), maxReportedSkips)]
	if v.Skipped == nil {
		v.Skipped = []csvimport.Skip{}
	}
	s.auditAction(ctx, r, "calendar.import", c.ID, fmt.Sprintf("imported=%d duplicates=%d skipped=%d", v.Imported, v.Duplicates, v.SkippedCount))
	s.writeJSON(w, http.StatusOK, v)
}
