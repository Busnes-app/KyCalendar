package kypost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
)

// Where a Peer came from.
const (
	SourceEnvironment = "environment" // KYCALENDAR_KYPOST_URL and _SECRET_FILE
	SourceKyIdentity  = "kyidentity"  // KyIdentity's suite directory
)

// ErrNotConnected is a send while no KyPost is linked.
var ErrNotConnected = errors.New("kypost: not connected")

// Peer is one KyPost: its https base URL (no trailing slash) and the shared secret.
type Peer struct {
	URL    string
	Secret []byte
	Source string
}

// Link is the KyPost connection, which KyIdentity can replace while the server runs. The secret
// lives only here, in memory.
type Link struct {
	HTTP *http.Client
	// Rotated is called when KyPost refuses the signature on a KyIdentity peer: the pair secret
	// may have rotated. It must not block.
	Rotated func()
	peer    atomic.Pointer[Peer]
}

// Peer is the linked KyPost, nil when none.
func (l *Link) Peer() *Peer { return l.peer.Load() }

// Set links p; nil unlinks.
func (l *Link) Set(p *Peer) { l.peer.Store(p) }

// Send is Client.Send to the linked KyPost. A signature KyPost refuses on a KyIdentity peer is
// worth a retry once the secret is fetched again.
func (l *Link) Send(ctx context.Context, id string, req SendRequest) error {
	return l.do(func(c *Client) error { return c.Send(ctx, id, req) })
}

// SendTaskState is Client.SendTaskState to the linked KyPost, with Send's rotation handling.
func (l *Link) SendTaskState(ctx context.Context, id string, req TaskStateRequest) error {
	return l.do(func(c *Client) error { return c.SendTaskState(ctx, id, req) })
}

func (l *Link) do(send func(*Client) error) error {
	p := l.Peer()
	if p == nil {
		return ErrNotConnected
	}
	err := send(&Client{BaseURL: p.URL, Secret: p.Secret, HTTP: l.HTTP})
	var refused Refused
	if p.Source == SourceKyIdentity && errors.As(err, &refused) && (refused.Code == "bad_signature" || refused.Code == "status_401") {
		if l.Rotated != nil {
			l.Rotated()
		}
		return fmt.Errorf("%w: %v", ErrRetry, err)
	}
	return err
}
