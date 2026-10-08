package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type groupView struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Source        string `json:"source"`
	MemberCount   int    `json:"member_count"`
	CalendarCount int    `json:"calendar_count"`
	// SCIMConflict marks a local group whose name SCIM tried and failed to create.
	SCIMConflict bool `json:"scim_conflict,omitempty"`
}

type memberView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
}

// cleanName trims a display name; ok is false when it is empty, over 255 bytes or holds a
// control character, a format character (Cf: zero-width, bidi overrides) or a line or
// paragraph separator, any of which can make two names look alike or reorder the screen.
func cleanName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, s != "" && len(s) <= 255 && strings.IndexFunc(s, invisible) < 0
}

func invisible(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// writeManaged answers a write to a synced person or group: its identity provider owns it.
func (s *Server) writeManaged(w http.ResponseWriter, what string) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "This " + what + " is managed by your identity provider; change it there", "code": "managed_externally"})
}

// grantCounts maps each group ID to the number of group calendars that grant it a role.
func (s *Server) grantCounts(ctx context.Context) (map[string]int, error) {
	cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, c := range cals {
		gs, err := s.store.Calendars().ListGrants(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			counts[g.GroupID]++
		}
	}
	return counts, nil
}

// adminGroup loads the {id} group: 404 when missing; with local set, 409 managed_externally for
// a SCIM group.
func (s *Server) adminGroup(w http.ResponseWriter, r *http.Request, local bool) *store.Group {
	g, err := s.store.Groups().GetGroupByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such group")
		return nil
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the group")
		return nil
	}
	if local && g.Source != store.GroupSourceLocal {
		s.writeManaged(w, "group")
		return nil
	}
	return g
}

func (s *Server) writeNameTaken(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusConflict, map[string]string{"error": "A group with this name already exists", "code": "name_taken"})
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	offset, limit := listPage(r)
	groups, total, err := s.store.Groups().ListGroups(r.Context(), offset, limit, "")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	counts, err := s.grantCounts(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count group calendars")
		return
	}
	settings, err := s.store.Settings().GetAllSettings(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		v := groupView{ID: g.ID, DisplayName: g.DisplayName, Source: g.Source, MemberCount: len(g.Members), CalendarCount: counts[g.ID]}
		v.SCIMConflict = g.Source == store.GroupSourceLocal && settings[scim.ConflictKey(g.DisplayName)] != ""
		out = append(out, v)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"groups": out, "total": total})
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	g := s.adminGroup(w, r, false)
	if g == nil {
		return
	}
	counts, err := s.grantCounts(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count group calendars")
		return
	}
	members := make([]memberView, 0, len(g.Members))
	for _, id := range g.Members {
		u, err := s.store.Users().GetUserByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to load members")
			return
		}
		members = append(members, memberView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Source: u.SSOProvider})
	}
	slices.SortFunc(members, func(a, b memberView) int {
		return strings.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username))
	})
	s.writeJSON(w, http.StatusOK, map[string]any{
		"id": g.ID, "display_name": g.DisplayName, "source": g.Source, "calendar_count": counts[g.ID], "members": members,
	})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name, ok := cleanName(body.DisplayName)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Name must be 1-255 bytes with no control or invisible characters")
		return
	}
	g := &store.Group{ID: "grp_" + crypto.RandomHex(12), DisplayName: name, Source: store.GroupSourceLocal}
	if err := s.store.Groups().CreateGroup(r.Context(), g); errors.Is(err, store.ErrAlreadyExists) {
		s.writeNameTaken(w)
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create the group")
		return
	}
	s.auditAction(r.Context(), r, "admin.group_create", g.ID, "name="+strconv.Quote(name))
	s.writeJSON(w, http.StatusCreated, groupView{ID: g.ID, DisplayName: g.DisplayName, Source: g.Source})
}

func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name, ok := cleanName(body.DisplayName)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Name must be 1-255 bytes with no control or invisible characters")
		return
	}
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	from := g.DisplayName
	g.DisplayName = name
	if err := s.store.Groups().UpdateGroup(r.Context(), g); errors.Is(err, store.ErrAlreadyExists) {
		s.writeNameTaken(w)
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to rename the group")
		return
	}
	// A new name frees the old one, resolving a SCIM clash on it; a case-only rename still clashes.
	if !strings.EqualFold(from, name) {
		if err := s.store.Settings().DeleteSetting(r.Context(), scim.ConflictKey(from)); err != nil {
			log.Printf("groups: SCIM conflict flag for %s not cleared: %v", g.ID, err)
		}
	}
	s.auditAction(r.Context(), r, "admin.group_rename", g.ID, "from="+strconv.Quote(from)+" to="+strconv.Quote(name))
	s.writeJSON(w, http.StatusOK, groupView{ID: g.ID, DisplayName: g.DisplayName, Source: g.Source, MemberCount: len(g.Members)})
}

// handleAddGroupMember adds an active everyday user to a local group; adding a member twice is
// not an error. Administrators never see calendars, so they cannot be members.
func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	u, err := s.store.Users().GetUserByID(r.Context(), r.PathValue("userId"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such person")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the person")
		return
	}
	if u.Role == "admin" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Administrators cannot be group members: they never see calendars", "code": "admin_member"})
		return
	}
	if u.Status != "active" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Only active people can join a group", "code": "inactive_member"})
		return
	}
	if slices.Contains(g.Members, u.ID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.Groups().AddGroupMember(r.Context(), g.ID, u.ID); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to add the member")
		return
	}
	s.auditAction(r.Context(), r, "admin.group_member_add", g.ID, "user="+strconv.Quote(u.ID))
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveGroupMember removes a member of a local group; removing a non-member is not an
// error. Access follows membership at the next request, so nothing else is revoked.
func (s *Server) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	userID := r.PathValue("userId")
	if !slices.Contains(g.Members, userID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.Groups().RemoveGroupMember(r.Context(), g.ID, userID); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to remove the member")
		return
	}
	s.auditAction(r.Context(), r, "admin.group_member_remove", g.ID, "user="+strconv.Quote(userID))
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteGroup deletes a local group with its memberships and calendar grants; the group
// calendars stay. A step-up action, detached so a dropped connection cannot lose the audit row.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "delete a group") {
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	g := s.adminGroup(w, r, true)
	if g == nil {
		return
	}
	counts, err := s.grantCounts(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to count group calendars")
		return
	}
	if err := s.store.Groups().DeleteGroup(r.Context(), g.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the group")
		return
	}
	if err := s.store.Settings().DeleteSetting(r.Context(), scim.ConflictKey(g.DisplayName)); err != nil {
		log.Printf("groups: SCIM conflict flag for %s not cleared: %v", g.ID, err)
	}
	s.auditAction(r.Context(), r, "admin.group_delete", g.ID, "name="+strconv.Quote(g.DisplayName)+" calendars="+strconv.Itoa(counts[g.ID]))
	w.WriteHeader(http.StatusNoContent)
}
