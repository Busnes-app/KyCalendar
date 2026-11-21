package kymessages

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Status routes (docs/SUITE-INTEGRATIONS.md section P), each with its own event type.
const (
	EventBusy      = "kycalendar.presence.busy"   // POST PresencePath + "calendar"
	EventMe        = "kycalendar.presence.me"     // POST PresencePath + "me"
	EventLookup    = "kycalendar.presence.lookup" // POST PresencePath + "lookup"
	PresencePath   = "/api/suite/presence/"
	MaxBusy        = 200
	MaxLookup      = 50
	BusyAhead      = 48 * time.Hour
	ManualMaxAhead = 7 * 24 * time.Hour
)

// Block is one busy block, RFC 3339 UTC. Nothing else about an event ever leaves KyCalendar.
type Block struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// BusyPush is a member's whole busy list for the next 48 hours.
type BusyPush struct {
	User
	Revision int64   `json:"revision"`
	Busy     []Block `json:"busy"`
}

// Manual is a status the member chose, until it expires.
type Manual struct {
	State string `json:"state"`
	Until string `json:"until"`
}

// Settings is a member's "Show my status to colleagues" switch and manual status.
type Settings struct {
	Share  bool    `json:"share"`
	Manual *Manual `json:"manual"`
}

// Status is one person's coarse status; Until is set for in_a_meeting and out_of_office.
type Status struct {
	Sub   string `json:"sub"`
	State string `json:"state"`
	Until string `json:"until,omitempty"`
}

// States KyMessages answers; anything else reads unknown.
var states = map[string]bool{"available": true, "busy": true, "in_a_meeting": true, "away": true, "out_of_office": true, "unknown": true}

// Busy sends p; the outcome is applied, stale or sharing_off (all done), or duplicate.
func (l *Link) Busy(ctx context.Context, eventID string, p BusyPush) (string, error) {
	if p.Busy == nil {
		p.Busy = []Block{}
	}
	if len(p.Busy) > MaxBusy {
		p.Busy = p.Busy[:MaxBusy]
	}
	reply, err := l.do(ctx, http.MethodPost, EventBusy, PresencePath+"calendar", eventID, p)
	if err != nil {
		return "", err
	}
	var out struct {
		Outcome string `json:"outcome"`
	}
	_ = json.Unmarshal(reply, &out)
	return out.Outcome, nil
}

// Me reads u's settings, after applying share (nil: leave) and manual (setManual false: leave;
// nil with setManual: clear).
func (l *Link) Me(ctx context.Context, eventID string, u User, share *bool, manual *Manual, setManual bool) (Settings, error) {
	body := map[string]any{"sub": u.Sub, "issuer": u.Issuer}
	if u.OrgID != "" {
		body["org_id"] = u.OrgID
	}
	if share != nil {
		body["share"] = *share
	}
	if setManual {
		body["manual"] = manual
	}
	var out Settings
	reply, err := l.do(ctx, http.MethodPost, EventMe, PresencePath+"me", eventID, body)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(reply, &out) != nil || (out.Manual != nil && !states[out.Manual.State]) {
		return Settings{}, Refused{Code: "bad_reply"}
	}
	return out, nil
}

// Lookup asks for up to MaxLookup people's status as u. The answer is keyed by subject, and
// only subjects asked for are kept.
func (l *Link) Lookup(ctx context.Context, eventID string, u User, subs []string) (map[string]Status, error) {
	if len(subs) > MaxLookup {
		subs = subs[:MaxLookup]
	}
	reply, err := l.do(ctx, http.MethodPost, EventLookup, PresencePath+"lookup", eventID, struct {
		User
		Subs []string `json:"subs"`
	}{u, subs})
	if err != nil {
		return nil, err
	}
	var out struct {
		People []Status `json:"people"`
	}
	if json.Unmarshal(reply, &out) != nil {
		return nil, Refused{Code: "bad_reply"}
	}
	asked := map[string]bool{}
	for _, s := range subs {
		asked[s] = true
	}
	m := map[string]Status{}
	for _, p := range out.People {
		if !asked[p.Sub] {
			continue
		}
		if !states[p.State] {
			p.State = "unknown"
		}
		if p.State != "in_a_meeting" && p.State != "out_of_office" {
			p.Until = ""
		} else if _, err := time.Parse(time.RFC3339, p.Until); err != nil {
			p.Until = ""
		}
		m[p.Sub] = p
	}
	return m, nil
}
