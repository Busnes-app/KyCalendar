package api_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/feed"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// TestMCPAuthorizationMatrix runs every tool under every MCP scope against each kind of
// calendar a token could name. Transport-level refusals (no token, a CalDAV token, an
// administrator, a browser Origin, a deactivated owner) are TestMCPAuthentication and
// TestMCPDeactivatedUserIsRefused. "" is success; anything else is the tool error expected.
func TestMCPAuthorizationMatrix(t *testing.T) {
	w := newMCPWorld(t)
	ctx := context.Background()

	loginAs(t, w.srv, w.st, "bob", "user")
	if err := davbackend.EnsureDefault(ctx, w.st, "usr_bob", 0); err != nil {
		t.Fatal(err)
	}
	bobs, _ := w.st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_bob")
	personal, _ := w.st.Calendars().GetCalendarByID(ctx, w.personal)
	sub := &store.Calendar{ID: "cal_alice_work", OwnerKind: "user", OwnerID: "usr_alice", Slug: "work", Name: "Work"}
	if err := w.st.Calendars().CreateSubscribedCalendar(ctx, sub, &store.Subscription{URLSealed: "x", URLHost: "h", NextCheckAt: time.Now().Add(time.Hour)}, 0, 0); err != nil {
		t.Fatal(err)
	}
	mirrored, _, err := feed.Parse(sub.ID, []byte(eventICS("work-uid")))
	if err != nil || w.st.Calendars().ReplaceSubscriptionObjects(ctx, sub.ID, mirrored, store.OwnerLimits{}) != nil {
		t.Fatalf("mirror: %v", err)
	}

	// One event in each target, none created by a token: the web or CalDAV wrote them.
	targets := map[string]struct {
		cal *store.Calendar
		uid string
	}{
		"own personal":   {personal, "web-uid"},
		"group (editor)": {w.group, "group-uid"},
		"another user's": {bobs[0], "bob-uid"},
		"own subscribed": {sub, "work-uid"},
	}
	for _, tg := range targets {
		if !tg.cal.Subscribed {
			putObject(t, w.st, tg.cal, tg.uid+".ics", tg.uid, eventICS(tg.uid), 0)
		}
	}

	const missing = "No such calendar"
	readable := map[string]string{"own personal": "", "group (editor)": missing, "another user's": missing, "own subscribed": ""}
	create := map[string]map[string]string{
		store.ScopeMCPRead:     {"own personal": api.MCPReadOnlyForTest, "group (editor)": api.MCPReadOnlyForTest, "another user's": api.MCPReadOnlyForTest, "own subscribed": api.MCPReadOnlyForTest},
		store.ScopeMCPWriteOwn: {"own personal": "", "group (editor)": missing, "another user's": missing, "own subscribed": api.MCPSubscribedForTest},
		store.ScopeMCPWriteAny: {"own personal": "", "group (editor)": missing, "another user's": missing, "own subscribed": api.MCPSubscribedForTest},
	}
	update := map[string]map[string]string{
		store.ScopeMCPRead:     create[store.ScopeMCPRead],
		store.ScopeMCPWriteOwn: {"own personal": api.MCPNotOwnForTest, "group (editor)": missing, "another user's": missing, "own subscribed": api.MCPSubscribedForTest},
		store.ScopeMCPWriteAny: {"own personal": "", "group (editor)": missing, "another user's": missing, "own subscribed": api.MCPSubscribedForTest},
	}

	for _, scope := range []string{store.ScopeMCPRead, store.ScopeMCPWriteOwn, store.ScopeMCPWriteAny} {
		_, tok := seedToken(t, w.st, "usr_alice", scope)
		cs := agent(t, w.srv, tok)

		var cals struct {
			Calendars []struct {
				ID string `json:"id"`
			} `json:"calendars"`
		}
		callTool(t, cs, "list_calendars", map[string]any{}, &cals)
		var ids []string
		for _, c := range cals.Calendars {
			ids = append(ids, c.ID)
		}
		slices.Sort(ids)
		if want := []string{sub.ID, personal.ID}; !slices.Equal(ids, slices.Sorted(slices.Values(want))) {
			t.Errorf("%s list_calendars: %v, want only alice's own %v", scope, ids, want)
		}

		for name, tg := range targets {
			got := callTool(t, cs, "list_events", map[string]any{"start": "2026-10-01T00:00:00Z", "end": "2026-11-01T00:00:00Z", "calendar_ids": []string{tg.cal.ID}}, nil)
			if got != readable[name] {
				t.Errorf("%s list_events on %s: %q, want %q", scope, name, got, readable[name])
			}
			got = callTool(t, cs, "create_event", map[string]any{"calendar_id": tg.cal.ID, "title": "agent", "start": "2026-10-08T09:00:00Z", "end": "2026-10-08T10:00:00Z", "zone": "UTC"}, nil)
			if got != create[scope][name] {
				t.Errorf("%s create_event on %s: %q, want %q", scope, name, got, create[scope][name])
			}
			o, err := w.st.Calendars().GetObjectByUID(ctx, tg.cal.ID, tg.uid)
			if err != nil {
				t.Fatal(err)
			}
			got = callTool(t, cs, "update_event", map[string]any{"calendar_id": tg.cal.ID, "uid": tg.uid, "etag": o.ETag, "title": "agent was here"}, nil)
			if got != update[scope][name] {
				t.Errorf("%s update_event on %s: %q, want %q", scope, name, got, update[scope][name])
			}
		}
	}
}
