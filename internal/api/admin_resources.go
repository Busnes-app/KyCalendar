package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/resource"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// resourceBody is a resource as an administrator writes it.
type resourceBody struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Location    string   `json:"location"`
	Capacity    int      `json:"capacity"`
	Tags        []string `json:"tags"`
	Audience    string   `json:"audience"`
	Groups      []string `json:"groups"`
	Approval    string   `json:"approval"`
	Approvers   []string `json:"approvers"`
	MaxMinutes  int      `json:"max_minutes"`
	HorizonDays int      `json:"horizon_days"`
	Enabled     bool     `json:"enabled"`
}

type personRef struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type groupRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type adminResourceView struct {
	resourceBody
	ID         string      `json:"id"`
	Address    string      `json:"address"`
	GroupRefs  []groupRef  `json:"group_refs"`
	PeopleRefs []personRef `json:"approver_refs"`
}

func (s *Server) adminResourceView(r *http.Request, res *resource.Resource) (adminResourceView, error) {
	v := adminResourceView{
		resourceBody: resourceBody{Kind: res.Kind, Name: res.Name, Location: res.Location, Capacity: res.Capacity, Tags: res.Tags,
			Audience: res.Audience, Groups: nonNil(res.Groups), Approval: res.Approval, Approvers: nonNil(res.Approvers),
			MaxMinutes: res.MaxMinutes, HorizonDays: res.HorizonDays, Enabled: res.Enabled},
		ID: res.ID, Address: resource.Address(res.ID).Email(), GroupRefs: []groupRef{}, PeopleRefs: []personRef{},
	}
	for _, id := range res.Groups {
		g, err := s.store.Groups().GetGroupByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		} else if err != nil {
			return v, err
		}
		v.GroupRefs = append(v.GroupRefs, groupRef{ID: g.ID, DisplayName: g.DisplayName})
	}
	for _, id := range res.Approvers {
		u, err := s.store.Users().GetUserByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		} else if err != nil {
			return v, err
		}
		v.PeopleRefs = append(v.PeopleRefs, personRef{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName})
	}
	return v, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	rs, err := s.store.Resources().ListResources(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list rooms and equipment")
		return
	}
	out := make([]adminResourceView, 0, len(rs))
	for _, res := range rs {
		v, err := s.adminResourceView(r, res)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to list rooms and equipment")
			return
		}
		out = append(out, v)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"resources": out})
}

func (s *Server) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	s.writeResource(w, r, nil)
}

func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	old, err := s.store.Resources().GetResource(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such room or equipment")
		return
	} else if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the room or equipment")
		return
	}
	s.writeResource(w, r, old)
}

// writeResource creates a resource (old nil) or replaces old's definition.
func (s *Server) writeResource(w http.ResponseWriter, r *http.Request, old *resource.Resource) {
	var b resourceBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	res := &resource.Resource{Kind: b.Kind, Name: b.Name, Location: b.Location, Capacity: b.Capacity, Tags: b.Tags,
		Audience: b.Audience, Groups: b.Groups, Approval: b.Approval, Approvers: b.Approvers,
		MaxMinutes: b.MaxMinutes, HorizonDays: b.HorizonDays, Enabled: b.Enabled}
	if err := resource.Clean(res); err != nil {
		var bad resource.ErrInvalid
		errors.As(err, &bad)
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Check the " + bad.Field + " field", "code": "invalid_" + bad.Field})
		return
	}
	var err error
	action, status := "admin.resource_create", http.StatusCreated
	if old == nil {
		res.ID = crypto.RandomHex(16)
		err = s.store.Resources().CreateResource(r.Context(), res)
	} else {
		res.ID, res.CreatedAt = old.ID, old.CreatedAt
		action, status = "admin.resource_update", http.StatusOK
		err = s.store.Resources().UpdateResource(r.Context(), res)
	}
	switch {
	case errors.Is(err, store.ErrAlreadyExists):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "A room or equipment with this name already exists", "code": "name_taken"})
		return
	case errors.Is(err, store.ErrUnknownGroup):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "One of the groups no longer exists", "code": "unknown_group"})
		return
	case errors.Is(err, store.ErrBadApprover):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Approvers must be active everyday people, not administrators", "code": "bad_approver"})
		return
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such room or equipment")
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to save the room or equipment")
		return
	}
	s.auditAction(r.Context(), r, action, res.ID, "kind="+res.Kind+" enabled="+strconv.FormatBool(res.Enabled)+
		" audience="+res.Audience+" approval="+res.Approval)
	v, err := s.adminResourceView(r, res)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Saved; failed to load it back")
		return
	}
	s.writeJSON(w, status, v)
}
