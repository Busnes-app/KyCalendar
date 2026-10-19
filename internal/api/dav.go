package api

import (
	"bytes"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
)

func isDAVPath(p string) bool {
	return p == "/.well-known/caldav" || strings.HasPrefix(p, davbackend.Prefix+"/")
}

// canonicalPath rejects decoded paths with empty or dot segments (e.g. from %2F): the fork
// classifies a request by the cleaned path but hands the backend the raw one.
func canonicalPath(p string) bool {
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c == p
}

// handleDAV serves CalDAV for the user withDAVAuth put in the context.
func (s *Server) handleDAV(w http.ResponseWriter, r *http.Request) {
	user := davUser(r.Context())
	if !canonicalPath(r.URL.Path) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, davbackend.Prefix+"/"); ok && rest != "" {
		if owner, _, _ := strings.Cut(rest, "/"); owner != user.ID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	grants, err := s.store.Calendars().UserGrants(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	limits := s.config.Calendar
	backend := &davbackend.Backend{
		Store: s.store, User: user, Grants: grants,
		MaxObjectsPerUser: limits.MaxObjectsPerUser, MaxCalendarsPerUser: limits.MaxCalendarsPerUser, MaxBytesPerUser: limits.MaxBytesPerUser,
		MaxBytesTotal: limits.MaxBytesTotal, Sched: s.sched,
	}
	if r.Method == http.MethodPut && !guardICalendarBody(w, r) {
		return
	}
	h := &caldav.Handler{Backend: backend, Prefix: davbackend.Prefix, MaxResourceSize: calendar.MaxObjectSize}
	h.ServeHTTP(w, r.WithContext(davbackend.WithIfMatch(r.Context(), r.Header.Get("If-Match"))))
}

// invalidCalendarData is the fork's own answer to a body it cannot decode.
const invalidCalendarData = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<error xmlns="DAV:"><valid-calendar-data xmlns="urn:ietf:params:xml:ns:caldav"></valid-calendar-data></error>`

// guardICalendarBody refuses a PUT body with a parameter value go-ical would take quadratic
// time to decode, before the fork decodes it, and puts the bytes back otherwise. The body is
// already capped by ServeHTTP's MaxBytesReader.
func guardICalendarBody(w http.ResponseWriter, r *http.Request) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "caldav: failed to read body: "+err.Error(), http.StatusBadRequest) // as the fork answers
		return false
	}
	if calendar.LongParams(raw) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8") // what clients get from the fork, which sets its type too late
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, invalidCalendarData)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}
