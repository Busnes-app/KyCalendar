package access

import "github.com/Busnes-app/kycalendar/internal/store"

// Role is what a user may do with one calendar; each role includes every role below it.
type Role int

const (
	None    Role = iota
	Reader       // sees events
	Editor       // creates, edits and deletes events
	Manager      // also renames, recolours and changes grants
	Owner        // a personal calendar's owner
)

// ParseRole reads a grant's role. Owner and None are never granted.
func ParseRole(s string) (Role, bool) {
	switch s {
	case "reader":
		return Reader, true
	case "editor":
		return Editor, true
	case "manager":
		return Manager, true
	}
	return None, false
}

func (r Role) CanRead() bool   { return r >= Reader }
func (r Role) CanWrite() bool  { return r >= Editor }
func (r Role) CanManage() bool { return r >= Manager }

// Resolve is u's role on c. grants must be u's own grants. Administrators get None on every
// calendar, because they never read events. Personal calendars are owner-only. A group calendar
// takes the highest role among the grants naming it.
func Resolve(u *store.User, c *store.Calendar, grants []store.CalendarGrant) Role {
	if u.Role == "admin" {
		return None
	}
	switch c.OwnerKind {
	case "user":
		if c.OwnerID == u.ID {
			return Owner
		}
	case "group":
		best := None
		for _, g := range grants {
			if r, ok := ParseRole(g.Role); ok && g.CalendarID == c.ID && r > best {
				best = r
			}
		}
		return best
	}
	return None
}
