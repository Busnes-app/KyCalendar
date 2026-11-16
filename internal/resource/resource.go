// Package resource holds the pure rules for rooms and equipment: their calendar user address and
// what a valid definition is. Storage and HTTP belong to the callers.
package resource

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Busnes-app/kycalendar/internal/itip"
)

// Domain is the address domain of every resource. RFC 2606 reserves .invalid, so no mail
// system routes it; the scheduler never queues a message to it either.
const Domain = "kycalendar.invalid"

// Bounds on a definition.
const (
	MaxName        = 100
	MaxLocation    = 200
	MaxTags        = 20
	MaxTag         = 40
	MaxCapacity    = 10000
	MinMinutes     = 5
	MaxMinutes     = 14 * 24 * 60
	MaxHorizonDays = 730
	MaxGroups      = 50
	MaxApprovers   = 20

	DefaultMinutes     = 24 * 60
	DefaultHorizonDays = 180
)

// Resource is a room or piece of equipment members book by adding it to an event.
type Resource struct {
	ID, Kind, Name, Location string
	Capacity                 int // 0: not stated
	Tags                     []string
	Audience                 string // Everyone or Groups
	Approval                 string // Auto or Approvers
	MaxMinutes, HorizonDays  int
	Enabled                  bool
	Groups, Approvers        []string // IDs
	CreatedAt, UpdatedAt     time.Time
}

// Kinds, audiences and approval policies.
const (
	Room      = "room"
	Equipment = "equipment"
	Everyone  = "everyone"
	Groups    = "groups"
	Auto      = "auto"
	Approvers = "approvers"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Address is resource id's calendar user address.
func Address(id string) itip.Address {
	return itip.Address("mailto:resource-" + id + "@" + Domain)
}

// IDOf is the resource ID an address names, if it names one.
func IDOf(a itip.Address) (string, bool) {
	local, ok := strings.CutSuffix(a.Email(), "@"+Domain)
	if !ok {
		return "", false
	}
	id, ok := strings.CutPrefix(local, "resource-")
	return id, ok && idPattern.MatchString(id)
}

// ErrInvalid is a definition outside the bounds; its message says which field.
type ErrInvalid struct{ Field string }

func (e ErrInvalid) Error() string { return "invalid " + e.Field }

var errDup = errors.New("duplicate")

// text trims s and reports whether it is at most max characters with no control, format or
// separator character and no double quote (go-ical cannot encode one in a CN parameter).
func text(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, utf8.RuneCountInString(s) <= max && strings.IndexFunc(s, func(r rune) bool {
		return r == '"' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
	}) < 0
}

// Clean validates and normalises a definition in place: trimmed text, tags deduplicated
// ignoring case, groups and approvers deduplicated. Groups and approvers are cleared when the
// audience or approval policy does not use them.
func Clean(r *Resource) error {
	var ok bool
	if r.Kind != Room && r.Kind != Equipment {
		return ErrInvalid{"kind"}
	}
	if r.Name, ok = text(r.Name, MaxName); !ok || r.Name == "" {
		return ErrInvalid{"name"}
	}
	if r.Location, ok = text(r.Location, MaxLocation); !ok {
		return ErrInvalid{"location"}
	}
	if r.Capacity < 0 || r.Capacity > MaxCapacity {
		return ErrInvalid{"capacity"}
	}
	if len(r.Tags) > MaxTags {
		return ErrInvalid{"tags"}
	}
	seen := map[string]bool{}
	tags := []string{}
	for _, t := range r.Tags {
		if t, ok = text(t, MaxTag); !ok || t == "" {
			return ErrInvalid{"tags"}
		}
		if k := strings.ToLower(t); !seen[k] {
			seen[k] = true
			tags = append(tags, t)
		}
	}
	r.Tags = tags
	if r.MaxMinutes < MinMinutes || r.MaxMinutes > MaxMinutes {
		return ErrInvalid{"max_minutes"}
	}
	if r.HorizonDays < 1 || r.HorizonDays > MaxHorizonDays {
		return ErrInvalid{"horizon_days"}
	}
	var err error
	switch r.Audience {
	case Everyone:
		r.Groups = nil
	case Groups:
		if r.Groups, err = ids(r.Groups, MaxGroups); err != nil || len(r.Groups) == 0 {
			return ErrInvalid{"groups"}
		}
	default:
		return ErrInvalid{"audience"}
	}
	switch r.Approval {
	case Auto:
		r.Approvers = nil
	case Approvers:
		if r.Approvers, err = ids(r.Approvers, MaxApprovers); err != nil || len(r.Approvers) == 0 {
			return ErrInvalid{"approvers"}
		}
	default:
		return ErrInvalid{"approval"}
	}
	return nil
}

func ids(in []string, max int) ([]string, error) {
	if len(in) > max {
		return nil, errDup
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range in {
		if id == "" || len(id) > 64 {
			return nil, errDup
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}
