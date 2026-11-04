package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
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
	// Components is what the calendar holds: VEVENT, VTODO or both.
	Components []string `json:"components"`
	// Subscription is set on a calendar that mirrors an ICS link. The link itself is never sent.
	Subscription *subscriptionView `json:"subscription,omitempty"`
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
		DAVPath: davbackend.Prefix + "/" + user.ID + "/calendars/" + seg + "/", Components: c.Components}
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
		v := s.viewOf(user, c, access.Resolve(user, c, grants))
		if c.Subscribed {
			sub, err := s.store.Calendars().GetSubscription(r.Context(), c.ID)
			if errors.Is(err, store.ErrNotFound) {
				continue // deleted since the list was read
			}
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
				return
			}
			v.Subscription = subscriptionViewOf(sub)
		}
		out = append(out, v)
	}
	s.writeJSON(w, http.StatusOK, out)
}

type calendarBody struct {
	Name        *string  `json:"name"`
	Color       *string  `json:"color"`
	Description *string  `json:"description"`
	Components  []string `json:"components"` // create only; empty is VEVENT
}

// decodeCalendarBody validates a create or patch at the boundary; a present name must not be blank.
func decodeCalendarBody(r *http.Request) (calendarBody, error) {
	var b calendarBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		return b, errors.New("Invalid JSON body")
	}
	err := b.check()
	return b, err
}

// check trims the name and validates every present field.
func (b *calendarBody) check() error {
	if b.Name != nil {
		trimmed := strings.TrimSpace(*b.Name)
		if trimmed == "" {
			return errors.New("Name must not be blank")
		}
		b.Name = &trimmed
	}
	return calendar.CheckProps(b.Name, b.Description, b.Color)
}

func (s *Server) handleCreateCalendar(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	b, err := decodeCalendarBody(r)
	if err == nil && b.Name == nil {
		err = errors.New("Name is required")
	}
	var comps []string
	if err == nil {
		comps, err = calendar.Components(b.Components)
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := &store.Calendar{ID: "cal_" + uuid.NewString(), OwnerKind: "user", OwnerID: user.ID, Slug: "c-" + crypto.RandomHex(4), Name: *b.Name, Components: comps}
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
	if err == nil && b.Components != nil {
		err = errors.New("Components are fixed when a calendar is created")
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Calendars().UpdateCalendar(r.Context(), c.ID, b.Name, b.Description, b.Color); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to update the calendar")
		return
	}
	if c.OwnerKind == "group" {
		s.auditAction(r.Context(), r, "calendar.update", c.ID, "")
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
	if apiErr := s.deleteMeetings(ctx, c, sessionUser(r.Context())); apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	if err := s.store.Calendars().DeleteCalendar(ctx, c.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the calendar")
		return
	}
	s.auditAction(ctx, r, "calendar.delete", c.ID, "")
	w.WriteHeader(http.StatusNoContent)
}

// deleteMeetings deletes c's meetings one by one through the scheduled delete path, before the
// calendar goes, so their guests get the cancellations (or the organizer an attendee's answer)
// that deleting each event would send. as plans the deletes: the calendar's owner, or nil in a
// group calendar (see groupOrganizer).
func (s *Server) deleteMeetings(ctx context.Context, c *store.Calendar, as *store.User) *apiError {
	if s.sched == nil || c.Subscribed {
		return nil // a subscription mirrors someone else's meetings: nothing is sent for them
	}
	objs, err := s.store.Calendars().ListObjects(ctx, c.ID)
	if err != nil {
		return &apiError{status: http.StatusInternalServerError, msg: "Failed to load the calendar's events"}
	}
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			continue
		}
		org, ok := itip.Organizer(cal)
		if !ok {
			continue
		}
		writer := as
		if writer == nil {
			if writer = s.groupOrganizer(ctx, c, org, o.UID); writer == nil {
				continue
			}
		}
		err = davbackend.Delete(ctx, s.store, c, o.Name, o.ETag, s.objectLimits(), s.sched.Plan(ctx, writer, nil))
		switch {
		case err == nil, errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrPreconditionFailed):
		case errors.Is(err, store.ErrQuotaExceeded):
			return &apiError{status: http.StatusInsufficientStorage, msg: "Too many cancellations are waiting to be sent; try again later", code: "quota"}
		default:
			return &apiError{status: http.StatusInternalServerError, msg: "Failed to cancel the calendar's meetings"}
		}
	}
	return nil
}

// groupOrganizer is who a group calendar's meeting is cancelled as: its organizer, when they are
// a user here and no other calendar they can edit holds the meeting. Anyone who can edit c can
// write any ORGANIZER into it, so a copy of a meeting the organizer keeps elsewhere is deleted
// without a message rather than cancelled as them; a copy in a calendar they only read is no
// sign of it. A copy naming a meeting nobody holds cancels nothing: delivery finds no attendee
// copy of that UID from that organizer.
func (s *Server) groupOrganizer(ctx context.Context, c *store.Calendar, org itip.Address, uid string) *store.User {
	u, err := s.store.Users().CalendarUser(ctx, org.Email())
	if err != nil {
		return nil // an organizer outside this instance: nothing is sent from here
	}
	grants, err := s.store.Calendars().UserGrants(ctx, u.ID)
	if err != nil {
		return nil
	}
	own, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", u.ID)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(own)+len(grants))
	for _, oc := range own {
		if !oc.Subscribed {
			ids = append(ids, oc.ID)
		}
	}
	for _, g := range grants {
		if gc, err := s.store.Calendars().GetCalendarByID(ctx, g.CalendarID); err == nil && access.CanEditEvents(gc, access.Resolve(u, gc, grants)) {
			ids = append(ids, g.CalendarID)
		}
	}
	for _, id := range ids {
		if id == c.ID {
			continue
		}
		if _, err := s.store.Calendars().GetObjectByUID(ctx, id, uid); !errors.Is(err, store.ErrNotFound) {
			return nil
		}
	}
	return u
}
