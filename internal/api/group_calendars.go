package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const maxListPage = 200

type grantView struct {
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
	Role      string `json:"role"`
}

type groupCalendarView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Color       string      `json:"color"`
	Description string      `json:"description"`
	CreatedAt   time.Time   `json:"created_at"`
	Grants      []grantView `json:"grants"`
}

func grantViews(gs []store.CalendarGrant) []grantView {
	out := make([]grantView, 0, len(gs))
	for _, g := range gs {
		out = append(out, grantView{GroupID: g.GroupID, GroupName: g.GroupName, Role: g.Role})
	}
	return out
}

func groupCalendarViewOf(c *store.Calendar, gs []store.CalendarGrant) groupCalendarView {
	return groupCalendarView{ID: c.ID, Name: c.Name, Color: c.Color, Description: c.Description, CreatedAt: c.CreatedAt, Grants: grantViews(gs)}
}

// auditAction records an admin or access change by the session user; IDs and names only.
func (s *Server) auditAction(ctx context.Context, r *http.Request, action, resource, details string) {
	actor := ""
	if u := sessionUser(r.Context()); u != nil {
		actor = u.ID
	}
	_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: actor, Action: action, Resource: resource, Details: details, IPAddress: s.requestIP(r)})
}

func listPage(r *http.Request) (offset, limit int) {
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > maxListPage {
		limit = maxListPage
	}
	return offset, limit
}

func (s *Server) handleListGroupCalendars(w http.ResponseWriter, r *http.Request) {
	cals, err := s.store.Calendars().ListCalendarsByKind(r.Context(), "group")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
		return
	}
	out := make([]groupCalendarView, 0, len(cals))
	for _, c := range cals {
		gs, err := s.store.Calendars().ListGrants(r.Context(), c.ID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to list grants")
			return
		}
		out = append(out, groupCalendarViewOf(c, gs))
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateGroupCalendar(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		s.writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if err := calendar.CheckProps(&body.Name, &body.Description, &body.Color); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := "cal_" + uuid.NewString()
	c := &store.Calendar{ID: id, OwnerKind: "group", OwnerID: id, Slug: "group", Name: body.Name, Color: body.Color, Description: body.Description}
	if err := s.store.Calendars().CreateCalendar(r.Context(), c, 0); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create the calendar")
		return
	}
	s.auditAction(r.Context(), r, "admin.calendar_create", c.ID, "")
	s.writeJSON(w, http.StatusCreated, groupCalendarViewOf(c, nil))
}

// handleDeleteGroupCalendar deletes a group calendar with every event in it. It is a step-up
// action: the session's credentials must be younger than stepUpWindow.
func (s *Server) handleDeleteGroupCalendar(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "delete a calendar") {
		return
	}
	// Irreversible: a dropped connection must not lose the audit row.
	ctx := context.WithoutCancel(r.Context())
	id := r.PathValue("id")
	c, err := s.store.Calendars().GetCalendarByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.OwnerKind != "group") {
		s.writeError(w, http.StatusNotFound, "No such group calendar")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return
	}
	if err := s.store.Calendars().DeleteCalendar(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the calendar")
		return
	}
	s.auditAction(ctx, r, "admin.calendar_delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	recs, total, err := s.store.Audit().ListAuditRecords(r.Context(), offset, limit)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list audit records")
		return
	}
	if recs == nil {
		recs = []*store.AuditRecord{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"records": recs, "total": total})
}

// grantableCalendar loads the group calendar named in the path if the session user may change
// its grants: administrators always, everyday users with the manager role. A user who cannot
// read it gets 404, a reader or editor 403.
func (s *Server) grantableCalendar(w http.ResponseWriter, r *http.Request) *store.Calendar {
	user := sessionUser(r.Context())
	c, err := s.store.Calendars().GetCalendarByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.OwnerKind != "group") {
		s.writeError(w, http.StatusNotFound, "No such group calendar")
		return nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return nil
	}
	if user.Role == "admin" {
		return c
	}
	grants, err := s.store.Calendars().UserGrants(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load access")
		return nil
	}
	switch role := access.Resolve(user, c, grants); {
	case !role.CanRead():
		s.writeError(w, http.StatusNotFound, "No such group calendar")
		return nil
	case !role.CanManage():
		s.writeError(w, http.StatusForbidden, "Only a manager can change who has access")
		return nil
	}
	return c
}

func (s *Server) writeGrants(w http.ResponseWriter, r *http.Request, calendarID string) {
	gs, err := s.store.Calendars().ListGrants(r.Context(), calendarID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list grants")
		return
	}
	s.writeJSON(w, http.StatusOK, grantViews(gs))
}

func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	if c := s.grantableCalendar(w, r); c != nil {
		s.writeGrants(w, r, c.ID)
	}
}

// grantGroup is the {group} path value, refused past the 64-byte ID column before any lookup.
func (s *Server) grantGroup(w http.ResponseWriter, r *http.Request) (string, bool) {
	group := r.PathValue("group")
	if len(group) > 64 {
		s.writeError(w, http.StatusBadRequest, "Group ID is too long")
		return "", false
	}
	return group, true
}

func (s *Server) handleSetGrant(w http.ResponseWriter, r *http.Request) {
	group, ok := s.grantGroup(w, r)
	if !ok {
		return
	}
	c := s.grantableCalendar(w, r)
	if c == nil {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if _, ok := access.ParseRole(body.Role); !ok {
		s.writeError(w, http.StatusBadRequest, "Role must be reader, editor or manager")
		return
	}
	err := s.store.Calendars().SetGrant(r.Context(), store.CalendarGrant{CalendarID: c.ID, GroupID: group, Role: body.Role})
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such group")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to change access")
		return
	}
	s.auditAction(r.Context(), r, "calendar.grant_set", c.ID, "group="+strconv.Quote(group)+" role="+strconv.Quote(body.Role))
	s.writeGrants(w, r, c.ID)
}

func (s *Server) handleDeleteGrant(w http.ResponseWriter, r *http.Request) {
	group, ok := s.grantGroup(w, r)
	if !ok {
		return
	}
	c := s.grantableCalendar(w, r)
	if c == nil {
		return
	}
	if err := s.store.Calendars().DeleteGrant(r.Context(), c.ID, group); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to remove access")
		return
	}
	s.auditAction(r.Context(), r, "calendar.grant_remove", c.ID, "group="+strconv.Quote(group))
	s.writeGrants(w, r, c.ID)
}
