package resource

import (
	"errors"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/itip"
)

func TestAddress(t *testing.T) {
	id := strings.Repeat("ab", 16)
	a := Address(id)
	if parsed, ok := itip.ParseAddress(string(a)); !ok || parsed != a {
		t.Fatalf("address %q does not parse", a)
	}
	if got, ok := IDOf(a); !ok || got != id {
		t.Fatalf("IDOf = %q %v", got, ok)
	}
	for _, bad := range []itip.Address{"mailto:resource-" + itip.Address(id) + "@example.com", "mailto:resource-xyz@kycalendar.invalid",
		"mailto:room-" + itip.Address(id) + "@kycalendar.invalid", "mailto:ann@kycalendar.invalid"} {
		if _, ok := IDOf(bad); ok {
			t.Errorf("IDOf(%q) accepted", bad)
		}
	}
}

func valid() *Resource {
	return &Resource{Kind: Room, Name: " Fjord ", Tags: []string{"Screen", "screen ", "video"}, Audience: Everyone,
		Groups: []string{"grp_x"}, Approval: Auto, Approvers: []string{"usr_x"}, MaxMinutes: 60, HorizonDays: 30}
}

func TestClean(t *testing.T) {
	r := valid()
	if err := Clean(r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "Fjord" || len(r.Tags) != 2 || r.Groups != nil || r.Approvers != nil {
		t.Fatalf("cleaned %+v", r)
	}
	for field, mutate := range map[string]func(*Resource){
		"kind":         func(r *Resource) { r.Kind = "car" },
		"name":         func(r *Resource) { r.Name = strings.Repeat("x", MaxName+1) },
		"location":     func(r *Resource) { r.Location = "a‮b" },
		"capacity":     func(r *Resource) { r.Capacity = -1 },
		"tags":         func(r *Resource) { r.Tags = []string{"a\"b"} },
		"max_minutes":  func(r *Resource) { r.MaxMinutes = MaxMinutes + 1 },
		"horizon_days": func(r *Resource) { r.HorizonDays = 0 },
		"groups":       func(r *Resource) { r.Audience, r.Groups = Groups, []string{""} },
		"approvers":    func(r *Resource) { r.Approval, r.Approvers = Approvers, nil },
		"audience":     func(r *Resource) { r.Audience = "some" },
	} {
		r := valid()
		mutate(r)
		var bad ErrInvalid
		if err := Clean(r); !errors.As(err, &bad) || bad.Field != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	r = valid()
	r.Audience, r.Groups = Groups, []string{"g1", "g1", "g2"}
	if err := Clean(r); err != nil || len(r.Groups) != 2 {
		t.Fatalf("dedupe groups: %v %v", err, r.Groups)
	}
}
