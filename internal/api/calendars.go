package api

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type calendarView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Role        string `json:"role"`
	DAVPath     string `json:"dav_path"`
}

func roleName(r access.Role) string {
	switch r {
	case access.Owner:
		return "owner"
	case access.Manager:
		return "manager"
	case access.Editor:
		return "editor"
	case access.Reader:
		return "reader"
	}
	return ""
}

func (s *Server) objectLimits() store.OwnerLimits {
	l := s.config.Calendar
	return store.OwnerLimits{MaxObjects: l.MaxObjectsPerUser, MaxBytes: l.MaxBytesPerUser, MaxTotalBytes: l.MaxBytesTotal}
}

func (s *Server) viewOf(user *store.User, c *store.Calendar, role access.Role) calendarView {
	kind, seg := "personal", c.Slug
	if c.OwnerKind == "group" {
		kind, seg = "group", "_"+c.ID
	}
	return calendarView{ID: c.ID, Name: c.Name, Color: c.Color, Description: c.Description, Kind: kind, Role: roleName(role),
		DAVPath: davbackend.Prefix + "/" + user.ID + "/calendars/" + seg + "/"}
}

// visibleCalendars lists the user's personal calendars (creating the default) and every group
// calendar a grant lets them read, with the user's grants for resolving roles.
func (s *Server) visibleCalendars(ctx context.Context, user *store.User) ([]*store.Calendar, []store.CalendarGrant, error) {
	if err := davbackend.EnsureDefault(ctx, s.store, user.ID, s.config.Calendar.MaxCalendarsPerUser); err != nil {
		return nil, nil, err
	}
	cals, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", user.ID)
	if err != nil {
		return nil, nil, err
	}
	grants, err := s.store.Calendars().UserGrants(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	var groups []*store.Calendar
	for _, g := range grants {
		if seen[g.CalendarID] {
			continue
		}
		seen[g.CalendarID] = true
		c, err := s.store.Calendars().GetCalendarByID(ctx, g.CalendarID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if access.Resolve(user, c, grants).CanRead() {
			groups = append(groups, c)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return append(cals, groups...), grants, nil
}

// calendarFor loads the calendar named id and the session user's role on it. A calendar the user
// cannot read answers 404, like one that does not exist; the caller checks the role it needs.
func (s *Server) calendarFor(w http.ResponseWriter, r *http.Request, id string) (*store.Calendar, access.Role) {
	user := sessionUser(r.Context())
	c, err := s.store.Calendars().GetCalendarByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such calendar")
		return nil, access.None
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return nil, access.None
	}
	grants, err := s.store.Calendars().UserGrants(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load access")
		return nil, access.None
	}
	role := access.Resolve(user, c, grants)
	if !role.CanRead() {
		s.writeError(w, http.StatusNotFound, "No such calendar")
		return nil, access.None
	}
	return c, role
}

func (s *Server) handleListCalendars(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	cals, grants, err := s.visibleCalendars(r.Context(), user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
		return
	}
	out := make([]calendarView, 0, len(cals))
	for _, c := range cals {
		out = append(out, s.viewOf(user, c, access.Resolve(user, c, grants)))
	}
	s.writeJSON(w, http.StatusOK, out)
}
