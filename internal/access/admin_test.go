package access_test

import (
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
)

// Only the exact app role makes an administrator. KyIdentity copies the global role into
// `roles` for an app that has no app roles yet, so "admin" must not count.
func TestIsAdminExactMatch(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"bare string list", []any{"kycalendar.admin"}, true},
		{"scim objects", []any{map[string]any{"value": "kycalendar.admin", "primary": true}}, true},
		{"single string", "kycalendar.admin", true},
		{"typed list", []string{"x", "kycalendar.admin"}, true},
		{"global admin", []any{"admin"}, false},
		{"other product", []any{"kypost.admin"}, false},
		{"case differs", []any{"KyCalendar.Admin"}, false},
		{"longer name", []any{"kycalendar.admin.extra"}, false},
		{"absent", nil, false},
		{"empty", []any{}, false},
		{"wrong shapes", []any{42, map[string]any{"value": 7}, []any{"kycalendar.admin"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := access.IsAdmin(access.RoleValues(tc.in)); got != tc.want {
				t.Fatalf("IsAdmin(RoleValues(%#v)) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
