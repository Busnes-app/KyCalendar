package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// A wrong secret is audited with the real token ID and never the secret, and the tenth failure
// from one address shuts the next request out with 429, even one bearing a valid token.
func TestMCPFailuresAreAuditedAndLimited(t *testing.T) {
	w := newMCPWorld(t)
	id, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	wrong := tok[:len(tok)-4] + "zzzz"

	for i := range 10 {
		if got := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + wrong}).Code; got != http.StatusUnauthorized {
			t.Fatalf("failure %d: got %d, want 401", i+1, got)
		}
	}
	limited := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + tok})
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("after 10 failures: got %d Retry-After %q, want 429 with Retry-After", limited.Code, limited.Header().Get("Retry-After"))
	}

	recs, _, err := w.st.Audit().ListAuditRecords(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var failed int
	for _, r := range recs {
		if r.Action != "mcp.auth_failed" {
			continue
		}
		failed++
		if r.Resource != "usr_alice" || r.Details != "app_password:"+id {
			t.Errorf("audit row %+v, want the owner and the token ID", r)
		}
	}
	all, _ := json.Marshal(recs)
	if failed != 10 || strings.Contains(string(all), wrong) || strings.Contains(string(all), tok) {
		t.Errorf("%d mcp.auth_failed rows (want 10); a secret in the audit log: %t", failed, strings.Contains(string(all), "zzzz"))
	}
}

// update_event has no way to cancel or hide an event: no status, no exception dates, no blank
// title. Unknown fields are refused or ignored, and the event stays as it was.
func TestMCPUpdateCannotCancel(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	var made writeOut
	if msg := callTool(t, cs, "create_event", map[string]any{"calendar_id": w.personal, "title": "Dentist", "start": "2026-10-20T09:00:00Z", "end": "2026-10-20T10:00:00Z", "zone": "UTC",
		"repeat": map[string]any{"freq": "weekly"}}, &made); msg != "" {
		t.Fatal(msg)
	}
	for _, extra := range []map[string]any{
		{"status": "CANCELLED"},
		{"exdate": "20261027T090000Z"},
		{"title": "   "},
	} {
		args := map[string]any{"calendar_id": w.personal, "uid": made.UID, "etag": made.ETag}
		for k, v := range extra {
			args[k] = v
		}
		_ = callTool(t, cs, "update_event", args, nil)
	}
	o, err := w.st.Calendars().GetObjectByUID(context.Background(), w.personal, made.UID)
	if err != nil {
		t.Fatal(err)
	}
	data := string(o.Data)
	if o.ETag != made.ETag || strings.Contains(data, "CANCELLED") || strings.Contains(data, "EXDATE") || !strings.Contains(data, "SUMMARY:Dentist") {
		t.Errorf("an update changed the event:\n%s", data)
	}
}

type writeOut struct {
	CalendarID string `json:"calendar_id"`
	UID        string `json:"uid"`
	ETag       string `json:"etag"`
}
