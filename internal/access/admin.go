// Package access decides what a user may do with a calendar. It is pure: callers load the
// user, the calendar and the user's grants, and ask.
package access

import "slices"

// AdminAppRole is the KyIdentity app role that makes an identity a KyCalendar administrator.
// Only an exact match counts: KyIdentity's global `role` is never a product admin.
const AdminAppRole = "kycalendar.admin"

// IsAdmin reports whether roles grant KyCalendar administration.
func IsAdmin(roles []string) bool { return slices.Contains(roles, AdminAppRole) }

// RoleValues reads a `roles` ID-token claim or SCIM attribute: one string, or a list of strings
// and {"value": string} objects. Any other shape contributes nothing.
func RoleValues(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case map[string]any:
		if s, ok := t["value"].(string); ok {
			return []string{s}
		}
	case []any:
		var out []string
		for _, e := range t {
			switch e := e.(type) {
			case string:
				out = append(out, e)
			case map[string]any:
				if s, ok := e["value"].(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}
