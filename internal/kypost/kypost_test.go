package kypost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

func TestSendIsSignedAndClassified(t *testing.T) {
	var got SendRequest
	status, reply := http.StatusOK, `{}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h := syncauth.FromRequest(r)
		if _, err := syncauth.Verify(secret, h, body, syncauth.Options{}); err != nil || h.EventType != EventSend || h.EventID != "msg-1" || r.URL.Path != SendPath {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Secret: secret, HTTP: srv.Client()}
	req := SendRequest{User: User{Sub: "s", Issuer: "https://id"}, From: "alice@example.com", Recipients: []string{"carol@example.net"}, Method: "REQUEST", ICS: "BEGIN:VCALENDAR"}
	if err := c.Send(context.Background(), "msg-1", req); err != nil || got.From != "alice@example.com" || got.Sub != "s" {
		t.Fatalf("send: %v %+v", err, got)
	}
	for _, c2 := range []struct {
		status int
		reply  string
		want   error
		code   string
	}{
		{409, `{"code":"not_attendee"}`, Refused{}, "not_attendee"},
		{403, `{"code":"Bad Code\r\nX: y"}`, Refused{}, "status_403"},
		{422, `not json`, Refused{}, "status_422"},
		{429, ``, ErrRetry, ""},
		{503, ``, ErrRetry, ""},
	} {
		status, reply = c2.status, c2.reply
		err := c.Send(context.Background(), "msg-1", req)
		var refused Refused
		switch {
		case c2.want == ErrRetry && !errors.Is(err, ErrRetry):
			t.Errorf("%d: %v, want a retry", c2.status, err)
		case c2.code != "" && (!errors.As(err, &refused) || refused.Code != c2.code):
			t.Errorf("%d: %v, want refused %q", c2.status, err, c2.code)
		}
	}
	dead := &Client{BaseURL: "https://127.0.0.1:1", Secret: secret, HTTP: &http.Client{Timeout: time.Second}}
	if err := dead.Send(context.Background(), "m", req); !errors.Is(err, ErrRetry) {
		t.Errorf("network failure: %v", err)
	}
}

func signed(t *testing.T, event, id string, body []byte) *http.Request {
	t.Helper()
	h, err := syncauth.Sign(secret, time.Now(), event, id, body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/itip/inbound", bytes.NewReader(body))
	h.Apply(r)
	return r
}

func TestVerify(t *testing.T) {
	replay := syncauth.NewMemoryReplay(syncauth.DefaultWindow, 10)
	body := []byte(`{"sub":"s"}`)
	if got, replayed, err := Verify(signed(t, EventInbound, "e1", body), secret, EventInbound, replay); err != nil || replayed || string(got) != string(body) {
		t.Fatalf("valid: %q %v %v", got, replayed, err)
	}
	if _, replayed, err := Verify(signed(t, EventInbound, "e1", body), secret, EventInbound, replay); err != nil || !replayed {
		t.Errorf("replay: %v %v", replayed, err)
	}
	if _, _, err := Verify(signed(t, EventRespond, "e2", body), secret, EventInbound, replay); err == nil {
		t.Error("another route's event type accepted")
	}
	if _, _, err := Verify(signed(t, EventInbound, "e3", body), []byte("ffffffffffffffffffffffffffffffff"), EventInbound, replay); err == nil {
		t.Error("another secret accepted")
	}
	unsigned := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	if _, _, err := Verify(unsigned, secret, EventInbound, replay); err == nil {
		t.Error("an unsigned request accepted")
	}
	huge := bytes.Repeat([]byte("a"), maxInbound+1)
	if _, _, err := Verify(signed(t, EventInbound, "e4", huge), secret, EventInbound, replay); !errors.Is(err, syncauth.ErrBodyTooLarge) {
		t.Errorf("oversize: %v", err)
	}
}
