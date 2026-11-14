package kymessages

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/syncauth"
)

var secret = []byte("kymessages-test-secret-0123456789abcdef")

const meetingID = "abcdefghijklmnop_-12"

// peer answers every request with status and body after checking the signature and the event
// type the method calls for.
func peer(t *testing.T, status int, body string) (*Link, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h := syncauth.FromRequest(r)
		want := map[string]string{http.MethodPut: EventPut, http.MethodDelete: EventClose}[r.Method]
		if _, err := syncauth.Verify(secret, h, b, syncauth.Options{}); err != nil || h.EventType != want {
			t.Errorf("%s %s: event %q, signature %v", r.Method, r.URL.Path, h.EventType, err)
		}
		seen = append(seen, r)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	l := &Link{HTTP: srv.Client()}
	l.Set(&Peer{URL: srv.URL, Secret: secret, Source: SourceEnvironment})
	return l, &seen
}

func TestPutSignsAndReadsTheLink(t *testing.T) {
	l, seen := peer(t, 200, `{"member_url":"https://chat.example/#/room/!abc:example","skipped_subs":["s2"]}`)
	res, err := l.Put(context.Background(), meetingID, "evt-1", PutRequest{User: User{Sub: "s1", Issuer: "https://id.example"}, Title: "Sync", MemberSubs: []string{"s2"}})
	if err != nil || res.MemberURL != "https://chat.example/#/room/!abc:example" || len(res.SkippedSubs) != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if r := (*seen)[0]; r.Method != http.MethodPut || r.URL.Path != "/api/meetings/"+meetingID {
		t.Fatalf("request %s %s", r.Method, r.URL.Path)
	}
	if err := l.Close(context.Background(), meetingID, "evt-2", User{Sub: "s1"}); err != nil {
		t.Fatal(err)
	}
	if r := (*seen)[1]; r.Method != http.MethodDelete || r.URL.Path != "/api/meetings/"+meetingID {
		t.Fatalf("request %s %s", r.Method, r.URL.Path)
	}
}

func TestPutRefusesAnUnsafeLink(t *testing.T) {
	for _, u := range []string{"http://chat.example/x", "javascript:alert(1)", "https://user:pw@chat.example/", "https://chat.example/\nx", ""} {
		l, _ := peer(t, 200, `{"member_url":`+strings.ReplaceAll(`"`+u+`"`, "\n", `\n`)+`}`)
		var refused Refused
		if _, err := l.Put(context.Background(), meetingID, "e", PutRequest{}); !errors.As(err, &refused) || refused.Code != "bad_reply" {
			t.Fatalf("%q: %v", u, err)
		}
	}
}

func TestOutcomes(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		retry  bool
		code   string
	}{
		{503, `{"code":"unavailable"}`, true, ""},
		{429, `{"code":"rate_limited"}`, true, ""},
		{404, `{"error":"x","code":"kycalendar_off"}`, true, ""},
		{404, `{"error":"x","code":"no_user"}`, false, "no_user"},
		{409, `{"code":"closed"}`, false, "closed"},
		{400, `{"code":"Bad Body!"}`, false, "status_400"},
		{401, `{"code":"bad_signature"}`, false, "bad_signature"}, // an environment secret is not rotated
	} {
		l, _ := peer(t, c.status, c.body)
		_, err := l.Put(context.Background(), meetingID, "e", PutRequest{})
		var refused Refused
		switch {
		case c.retry && !errors.Is(err, ErrRetry):
			t.Errorf("%d %s: %v, want retry", c.status, c.body, err)
		case !c.retry && (!errors.As(err, &refused) || refused.Code != c.code):
			t.Errorf("%d %s: %v, want %s", c.status, c.body, err, c.code)
		}
	}
}

func TestRefusedSignatureOnAKyIdentityPeerAsksForALookup(t *testing.T) {
	l, _ := peer(t, 401, `{"code":"bad_signature"}`)
	l.Set(&Peer{URL: l.Peer().URL, Secret: secret, Source: SourceKyIdentity})
	rotated := 0
	l.Rotated = func() { rotated++ }
	if _, err := l.Put(context.Background(), meetingID, "e", PutRequest{}); !errors.Is(err, ErrRetry) || rotated != 1 {
		t.Fatalf("err %v rotated %d", err, rotated)
	}
}

func TestNotConnectedAndBadID(t *testing.T) {
	var l Link
	if _, err := l.Put(context.Background(), meetingID, "e", PutRequest{}); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	l2, seen := peer(t, 200, `{}`)
	for _, id := range []string{"short", strings.Repeat("a", 65), "abcdefghijklmnop/../x", "abcdefghijklmnop?x"} {
		var refused Refused
		if err := l2.Close(context.Background(), id, "e", User{}); !errors.As(err, &refused) || refused.Code != "bad_id" {
			t.Fatalf("%q: %v", id, err)
		}
	}
	if len(*seen) != 0 {
		t.Fatal("a bad ID reached KyMessages")
	}
}

func TestTitle(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "Meeting",
		"  \t ":                  "Meeting",
		"Weekly\nsync\x00":       "Weekly sync",
		strings.Repeat("é", 200): strings.Repeat("é", 127),
		strings.Repeat("a", 300): strings.Repeat("a", 255),
		"Plan ✓":                 "Plan ✓",
	} {
		if got := Title(in); got != want || len(got) > MaxTitle {
			t.Errorf("Title(%q) = %q", in, got)
		}
	}
}
