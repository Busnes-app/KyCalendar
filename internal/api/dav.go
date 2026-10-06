package api

import (
	"net/http"
	"strings"

	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
)

func isDAVPath(p string) bool {
	return p == "/.well-known/caldav" || strings.HasPrefix(p, davbackend.Prefix+"/")
}

// handleDAV serves CalDAV for the user withDAVAuth put in the context.
func (s *Server) handleDAV(w http.ResponseWriter, r *http.Request) {
	user := davUser(r.Context())
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
	}
	h := &caldav.Handler{Backend: backend, Prefix: davbackend.Prefix, MaxResourceSize: calendar.MaxObjectSize}
	h.ServeHTTP(w, r.WithContext(davbackend.WithIfMatch(r.Context(), r.Header.Get("If-Match"))))
}
