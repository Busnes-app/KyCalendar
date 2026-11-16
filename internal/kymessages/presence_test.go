package kymessages

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

var who = User{Sub: "s1", Issuer: "https://id.example"}

// The signed body carries start, end and the user: never a title or attendees.
func TestBusySendsTimesOnly(t *testing.T) {
	l, seen := peer(t, 200, `{"outcome":"applied"}`)
	out, err := l.Busy(context.Background(), "evt-1", BusyPush{User: who, Revision: 7, Busy: []Block{{Start: "2026-11-16T14:00:00Z", End: "2026-11-16T15:00:00Z"}}})
	if err != nil || out != "applied" {
		t.Fatalf("%q %v", out, err)
	}
	raw, _ := io.ReadAll((*seen)[0].Body)
	var b map[string]json.RawMessage
	if json.Unmarshal(raw, &b) != nil || len(b) != 4 || b["sub"] == nil || b["issuer"] == nil || b["revision"] == nil || b["busy"] == nil {
		t.Fatalf("body %s", raw)
	}
	if string(b["busy"]) != `[{"start":"2026-11-16T14:00:00Z","end":"2026-11-16T15:00:00Z"}]` {
		t.Fatalf("busy %s", b["busy"])
	}
	// No blocks is an empty list, never null: KyMessages refuses a missing one.
	l.Busy(context.Background(), "evt-2", BusyPush{User: who, Revision: 8})
	if raw, _ := io.ReadAll((*seen)[1].Body); !strings.Contains(string(raw), `"busy":[]`) {
		t.Fatalf("empty: %s", raw)
	}
}

func TestMeSendsOnlyWhatChanges(t *testing.T) {
	l, seen := peer(t, 200, `{"share":true,"manual":{"state":"away","until":"2026-11-16T15:00:00Z"}}`)
	got, err := l.Me(context.Background(), "e1", who, nil, nil, false)
	if err != nil || !got.Share || got.Manual == nil || got.Manual.State != "away" {
		t.Fatalf("%+v %v", got, err)
	}
	off := false
	l.Me(context.Background(), "e2", who, &off, nil, true)
	for i, want := range []string{`{"issuer":"https://id.example","sub":"s1"}`, `{"issuer":"https://id.example","manual":null,"share":false,"sub":"s1"}`} {
		if raw, _ := io.ReadAll((*seen)[i].Body); string(raw) != want {
			t.Errorf("request %d: %s", i, raw)
		}
	}
}

func TestLookupKeepsOnlyWhatWasAsked(t *testing.T) {
	l, _ := peer(t, 200, `{"people":[{"sub":"a","state":"in_a_meeting","until":"2026-11-16T15:00:00Z"},{"sub":"b","state":"on_fire"},
{"sub":"c","state":"busy","until":"2026-11-16T15:00:00Z"},{"sub":"x","state":"available"}]}`)
	got, err := l.Lookup(context.Background(), "e", who, []string{"a", "b", "c"})
	if err != nil || len(got) != 3 || got["a"].Until == "" || got["b"].State != "unknown" || got["c"].Until != "" {
		t.Fatalf("%+v %v", got, err)
	}
}
