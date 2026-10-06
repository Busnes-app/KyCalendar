package api

import (
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
	limits := s.config.Calendar
	backend := &davbackend.Backend{
		Store: s.store, User: user,
		MaxObjectsPerUser: limits.MaxObjectsPerUser, MaxCalendarsPerUser: limits.MaxCalendarsPerUser, MaxBytesPerUser: limits.MaxBytesPerUser,
		MaxBytesTotal: limits.MaxBytesTotal,
	}
	h := &caldav.Handler{Backend: backend, Prefix: davbackend.Prefix, MaxResourceSize: calendar.MaxObjectSize}
	h.ServeHTTP(w, r.WithContext(davbackend.WithIfMatch(r.Context(), r.Header.Get("If-Match"))))
}
