// Package kypost is the one definition of what KyCalendar and KyPost send each other about
// invitations, and KyCalendar's client for it. Every request is signed with
// ky-primitives/syncauth (scheme v1) under one shared secret; event types differ per direction,
// so a request cannot be reflected back at its sender.
package kypost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
)

// Event types, one per route and direction.
const (
	EventSend    = "kycalendar.itip.send" // KyCalendar to KyPost: mail this message as the user
	EventInbound = "kypost.itip.inbound"  // KyPost to KyCalendar: this arrived in the user's mailbox
	EventRespond = "kypost.itip.respond"  // KyPost to KyCalendar: the user answered an invitation
	SendPath     = "/api/calendar/itip/send"
	maxReply     = 64 << 10
	maxInbound   = 2 << 20 // a calendar object plus its JSON envelope
)

// User names who a request speaks for: their KyIdentity subject and issuer URL.
type User struct {
	Sub    string `json:"sub"`
	Issuer string `json:"issuer"`
}

// SendRequest asks KyPost to mail one iTIP message from the user's mailbox. KyPost builds the
// message itself and refuses a sender address or recipient the object does not justify.
type SendRequest struct {
	User
	From       string   `json:"from"`       // the organizer's (or replying attendee's) email
	Recipients []string `json:"recipients"` // emails
	Method     string   `json:"method"`     // REQUEST, CANCEL or REPLY
	ICS        string   `json:"ics"`
}

// InboundRequest is an iTIP message that arrived in the user's mailbox. KyPost forwards only
// mail whose DKIM signature is aligned with its From domain and covers From.
type InboundRequest struct {
	User
	From        string `json:"from"`
	DKIMAligned bool   `json:"dkim_aligned"`
	ICS         string `json:"ics"`
}

// RespondRequest is the user's answer from a KyPost RSVP card. ICS, the invitation as mailed, is
// required: the answer is bound to its organizer.
type RespondRequest struct {
	User
	UID          string `json:"uid"`
	RecurrenceID string `json:"recurrence_id,omitempty"`
	Partstat     string `json:"partstat"`
	ICS          string `json:"ics"`
}

// ErrRetry is a failure worth retrying: the network, 429 or a 5xx.
var ErrRetry = errors.New("kypost: try again later")

// Refused is KyPost refusing a message for good; Code is its reason.
type Refused struct{ Code string }

func (r Refused) Error() string { return "kypost refused: " + r.Code }

// Client sends to one KyPost.
type Client struct {
	BaseURL string // https://host, no trailing slash
	Secret  []byte
	HTTP    *http.Client
	Now     func() time.Time
}

// Send delivers req under event id (stable across retries of one message, so KyPost's replay
// guard turns a retry into an acknowledgement).
func (c *Client) Send(ctx context.Context, id string, req SendRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	h, err := syncauth.Sign(c.Secret, now(), EventSend, id, body)
	if err != nil {
		return err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+SendPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hr.Header.Set("Content-Type", "application/json")
	h.Apply(hr)
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRetry, err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: status %d", ErrRetry, resp.StatusCode)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(reply, &out)
	if !validCode(out.Code) {
		out.Code = fmt.Sprintf("status_%d", resp.StatusCode)
	}
	return Refused{Code: out.Code}
}

// validCode admits a short lowercase reason code; anything else from the peer is dropped.
func validCode(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789_") == ""
}

// Verify checks a request from KyPost against want (an event type), the secret and replay, and
// returns its body. Replayed reports a valid signature on an event already accepted: the
// sender retried after a lost response, and the caller acknowledges it without acting again.
func Verify(r *http.Request, secret []byte, want string, replay syncauth.Replay) (body []byte, replayed bool, err error) {
	body, err = io.ReadAll(io.LimitReader(r.Body, maxInbound+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > maxInbound {
		return nil, false, syncauth.ErrBodyTooLarge
	}
	h := syncauth.FromRequest(r)
	if h.EventType != want {
		return nil, false, syncauth.ErrBadSignature
	}
	if _, err := syncauth.Verify(secret, h, body, syncauth.Options{Replay: replay}); err != nil {
		if errors.Is(err, syncauth.ErrReplay) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return body, false, nil
}
