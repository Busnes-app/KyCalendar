package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// D10: every body KyCalendar sends KyMessages names the member's org in multi-tenant mode, and
// none in single-tenant mode.
func TestKyMessagesBodiesNameTheOrgInMultiTenantMode(t *testing.T) {
	r := newPresenceRig(t)
	r.reply = func(path string) (int, string) {
		if strings.HasSuffix(path, "/me") {
			return 200, `{"share":true,"manual":null}`
		}
		return 200, `{"outcome":"applied","people":[]}`
	}
	exercise := func() {
		t.Helper()
		r.clock = r.clock.Add(time.Hour) // past the last write and the status cache
		r.put(t, "m-"+r.clock.Format("150405"), time.Hour, 2*time.Hour)
		r.s.SyncPresence(context.Background())
		for _, c := range []struct {
			path, body string
			h          http.HandlerFunc
		}{
			{"/api/presence/me", "", r.s.handlePresenceMe},
			{"/api/presence", `{"emails":["bob@example.com"]}`, r.s.handlePresenceLookup},
		} {
			method := http.MethodGet
			if c.body != "" {
				method = http.MethodPost
			}
			if code, body := r.do(t, r.ann, method, c.path, c.body, c.h); code != 200 {
				t.Fatalf("%s: %d %s", c.path, code, body)
			}
		}
	}
	exercise()
	single := r.calls()
	if len(single) != 3 {
		t.Fatalf("single-tenant calls %d, want push, me and lookup", len(single))
	}
	for _, c := range single {
		if strings.Contains(string(c.Body), "org_id") {
			t.Errorf("single-tenant %s names an org: %s", c.Path, c.Body)
		}
	}

	if err := r.s.store.Settings().SetSetting(context.Background(), store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	exercise()
	multi := r.calls()[len(single):]
	if len(multi) != 3 {
		t.Fatalf("multi-tenant calls %d, want push, me and lookup", len(multi))
	}
	want := `"org_id":"` + string(store.OrgOf(r.ann)) + `"`
	for _, c := range multi {
		if !strings.Contains(string(c.Body), want) {
			t.Errorf("multi-tenant %s lacks %s: %s", c.Path, want, c.Body)
		}
	}
}
