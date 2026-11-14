// Package kymessages is what KyCalendar sends KyMessages about video meetings, and its client.
// Every request is signed with ky-primitives/syncauth (scheme v1) under the suite pair secret;
// each route has its own event type, so one request cannot stand in for another.
package kymessages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Busnes-app/ky-primitives/syncauth"
)

// Event types, one per route.
const (
	EventPut   = "kycalendar.meeting.put"   // PUT /api/meetings/{id}: create or reconcile the room
	EventClose = "kycalendar.meeting.close" // DELETE /api/meetings/{id}: close it for good
	MeetPath   = "/api/meetings/"
	MaxMembers = 100
	MaxTitle   = 255
	maxReply   = 64 << 10
	maxURL     = 2048
)

// Where a Peer came from.
const (
	SourceEnvironment = "environment" // KYCALENDAR_KYMESSAGES_URL and _SECRET_FILE
	SourceKyIdentity  = "kyidentity"  // KyIdentity's suite directory
)

var (
	// ErrRetry is a failure worth retrying: the network, 429, a 5xx or a refused signature.
	ErrRetry = errors.New("kymessages: try again later")
	// ErrNotConnected is a request while no KyMessages is linked.
	ErrNotConnected = errors.New("kymessages: not connected")
)

// Refused is KyMessages refusing a request for good; Code is its reason.
type Refused struct{ Code string }

func (r Refused) Error() string { return "kymessages refused: " + r.Code }

// User names the organizer: their KyIdentity subject and issuer URL.
type User struct {
	Sub    string `json:"sub"`
	Issuer string `json:"issuer"`
}

// PutRequest names the room and who may join it.
type PutRequest struct {
	User
	Title      string   `json:"title"`
	MemberSubs []string `json:"member_subs"`
}

// PutResult is KyMessages' answer: the room's link for members, and the subjects it skipped
// because they have no KyMessages account yet.
type PutResult struct {
	MemberURL   string   `json:"member_url"`
	SkippedSubs []string `json:"skipped_subs"`
}

// Peer is one KyMessages console: its https base URL (no trailing slash) and the pair secret.
type Peer struct {
	URL    string
	Secret []byte
	Source string
}

// Link is the KyMessages connection, which KyIdentity can replace while the server runs. The
// secret lives only here, in memory.
type Link struct {
	HTTP *http.Client
	// Rotated is called when KyMessages refuses the signature on a KyIdentity peer. It must not block.
	Rotated func()
	peer    atomic.Pointer[Peer]
}

// Peer is the linked KyMessages, nil when none.
func (l *Link) Peer() *Peer { return l.peer.Load() }

// Set links p; nil unlinks.
func (l *Link) Set(p *Peer) { l.peer.Store(p) }

// ValidID reports whether id is a meeting ID the contract allows: [A-Za-z0-9_-]{16,64}.
func ValidID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	return strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == ""
}

// Title makes s a valid room name: control characters become spaces, at most MaxTitle bytes on
// a rune boundary, and "Meeting" when nothing is left.
func Title(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	for len(s) > MaxTitle {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	if s = strings.TrimSpace(s); s == "" {
		return "Meeting"
	}
	return s
}

// Put creates or reconciles meeting id. eventID must be fresh per attempt: KyMessages answers a
// replayed one with {"outcome":"duplicate"} and no link.
func (l *Link) Put(ctx context.Context, id, eventID string, req PutRequest) (PutResult, error) {
	var out PutResult
	reply, err := l.do(ctx, http.MethodPut, EventPut, id, eventID, req)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(reply, &out) != nil || !ValidURL(out.MemberURL) {
		return PutResult{}, Refused{Code: "bad_reply"}
	}
	return out, nil
}

// Close closes meeting id; an unknown or already closed ID is success too.
func (l *Link) Close(ctx context.Context, id, eventID string, u User) error {
	_, err := l.do(ctx, http.MethodDelete, EventClose, id, eventID, u)
	return err
}

func (l *Link) do(ctx context.Context, method, eventType, id, eventID string, v any) ([]byte, error) {
	p := l.Peer()
	if p == nil {
		return nil, ErrNotConnected
	}
	if !ValidID(id) {
		return nil, Refused{Code: "bad_id"}
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	h, err := syncauth.Sign(p.Secret, time.Now(), eventType, eventID, body)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, method, p.URL+MeetPath+id, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	h.Apply(hr)
	resp, err := l.HTTP.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRetry, err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return reply, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrRetry, resp.StatusCode)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(reply, &out)
	if !validCode(out.Code) {
		out.Code = fmt.Sprintf("status_%d", resp.StatusCode)
	}
	// Unpaired on their side may be a pairing still settling; a refused signature on a KyIdentity
	// peer may be a rotated secret, worth a lookup and a retry.
	if out.Code == "kycalendar_off" {
		return nil, fmt.Errorf("%w: %s", ErrRetry, out.Code)
	}
	if resp.StatusCode == http.StatusUnauthorized && p.Source == SourceKyIdentity {
		if l.Rotated != nil {
			l.Rotated()
		}
		return nil, fmt.Errorf("%w: %s", ErrRetry, out.Code)
	}
	return nil, Refused{Code: out.Code}
}

// validCode admits a short lowercase reason code; anything else from the peer is dropped.
func validCode(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789_") == ""
}

// ValidURL admits a room link: absolute https, no credentials, bounded. It is written into
// events and shown as a Join button, so nothing else gets through.
func ValidURL(s string) bool {
	if len(s) > maxURL || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
