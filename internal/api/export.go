package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calexport"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var exportUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// exportedCalendar is one manifest.json entry of a member's export.
type exportedCalendar struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Items    int      `json:"items"`
	Unmerged []string `json:"unmerged,omitempty"`
}

// handleExport is GET /api/export: a zip of the caller's own calendars, one .ics each, with
// every object that cannot be merged copied as stored beside it, and manifest.json (tenancy
// spec, Export). It is the one product route an export-only session reaches.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := sessionUser(ctx)
	cals, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", user.ID)
	if err != nil {
		s.writeUnavailable(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="kycalendar-export.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	put := func(name string, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now().UTC()})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	manifest := []exportedCalendar{}
	used := map[string]bool{"manifest.json": true}
	unique := func(path string) string {
		p := path
		for n := 2; used[strings.ToLower(p)]; n++ {
			p = fmt.Sprintf("%s~%d", path, n)
		}
		used[strings.ToLower(p)] = true
		return p
	}
	// The headers are sent with the first byte, so a failure past here can only cut the zip
	// short, which a zip reader reports as damaged; it is logged, never served as complete.
	fail := func(err error) {
		log.Printf("export: user %s: %v", user.ID, err)
	}
	for _, c := range cals {
		body, items, unmerged, err := calexport.CalendarICS(ctx, s.store, c.ID)
		if err != nil {
			fail(err)
			return
		}
		e := exportedCalendar{ID: c.ID, Name: c.Name, File: unique("calendars/" + exportUnsafe.ReplaceAllString(c.ID, "_") + ".ics"), Items: items}
		if err := put(e.File, body); err != nil {
			fail(err)
			return
		}
		for _, o := range unmerged {
			f := unique(strings.TrimSuffix(e.File, ".ics") + ".unmerged/" + exportUnsafe.ReplaceAllString(o.Name, "_"))
			if err := put(f, o.Data); err != nil {
				fail(err)
				return
			}
			e.Unmerged = append(e.Unmerged, f)
		}
		manifest = append(manifest, e)
	}
	m, err := json.MarshalIndent(struct {
		UserID     string             `json:"user_id"`
		ExportedAt time.Time          `json:"exported_at"`
		Calendars  []exportedCalendar `json:"calendars"`
	}{user.ID, time.Now().UTC(), manifest}, "", "  ")
	if err == nil {
		err = put("manifest.json", append(m, '\n'))
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		fail(err)
		return
	}
	_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: user.ID, Action: "calendar.exported", Resource: "user:" + user.ID})
}
