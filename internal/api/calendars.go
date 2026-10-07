package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/google/uuid"
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
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Name < groups[j].Name || groups[i].Name == groups[j].Name && groups[i].ID < groups[j].ID
	})
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

type calendarBody struct {
	Name        *string `json:"name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
}

// decodeCalendarBody validates a create or patch at the boundary; a present name must not be blank.
func decodeCalendarBody(r *http.Request) (calendarBody, error) {
	var b calendarBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		return b, errors.New("Invalid JSON body")
	}
	if b.Name != nil {
		trimmed := strings.TrimSpace(*b.Name)
		if trimmed == "" {
			return b, errors.New("Name must not be blank")
		}
		b.Name = &trimmed
	}
	if err := calendar.CheckProps(b.Name, b.Description, b.Color); err != nil {
		return b, err
	}
	return b, nil
}

func (s *Server) handleCreateCalendar(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	b, err := decodeCalendarBody(r)
	if err == nil && b.Name == nil {
		err = errors.New("Name is required")
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := &store.Calendar{ID: "cal_" + uuid.NewString(), OwnerKind: "user", OwnerID: user.ID, Slug: "c-" + crypto.RandomHex(4), Name: *b.Name}
	if b.Color != nil {
		c.Color = *b.Color
	}
	if b.Description != nil {
		c.Description = *b.Description
	}
	err = s.store.Calendars().CreateCalendar(r.Context(), c, s.config.Calendar.MaxCalendarsPerUser)
	switch {
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "You have reached the calendar limit", "code": "quota"})
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to create the calendar")
		return
	}
	s.writeJSON(w, http.StatusCreated, s.viewOf(user, c, access.Owner))
}

func (s *Server) handlePatchCalendar(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if !role.CanManage() {
		s.writeError(w, http.StatusForbidden, "Only the owner or a manager can change this calendar")
		return
	}
	b, err := decodeCalendarBody(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Calendars().UpdateCalendar(r.Context(), c.ID, b.Name, b.Description, b.Color); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to update the calendar")
		return
	}
	if c.OwnerKind == "group" {
		s.auditCalendar(r.Context(), r, "calendar.update", c.ID, "")
	}
	updated, err := s.store.Calendars().GetCalendarByID(r.Context(), c.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return
	}
	s.writeJSON(w, http.StatusOK, s.viewOf(sessionUser(r.Context()), updated, role))
}

// handleDeleteCalendar deletes a personal calendar with its events. It runs detached, like the
// group calendar delete, so a dropped connection cannot leave it half-reported.
func (s *Server) handleDeleteCalendar(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if role != access.Owner {
		s.writeError(w, http.StatusForbidden, "Group calendars are deleted by an administrator")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.store.Calendars().DeleteCalendar(ctx, c.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the calendar")
		return
	}
	s.auditCalendar(ctx, r, "calendar.delete", c.ID, "")
	w.WriteHeader(http.StatusNoContent)
}
