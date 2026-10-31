package kypost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLinkSend(t *testing.T) {
	refuse := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"bad_signature"}`))
	}))
	defer refuse.Close()
	rotated := 0
	l := &Link{HTTP: refuse.Client(), Rotated: func() { rotated++ }}

	if err := l.Send(context.Background(), "id", SendRequest{}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("unlinked: %v", err)
	}
	// An environment secret cannot rotate under us: the refusal stands.
	l.Set(&Peer{URL: refuse.URL, Secret: secret, Source: SourceEnvironment})
	var refused Refused
	if err := l.Send(context.Background(), "id", SendRequest{}); !errors.As(err, &refused) || refused.Code != "bad_signature" || rotated != 0 {
		t.Errorf("environment peer: %v, rotated %d", err, rotated)
	}
	// KyIdentity's may have: ask again and retry the message.
	l.Set(&Peer{URL: refuse.URL, Secret: secret, Source: SourceKyIdentity})
	if err := l.Send(context.Background(), "id", SendRequest{}); !errors.Is(err, ErrRetry) || rotated != 1 {
		t.Errorf("KyIdentity peer: %v, rotated %d", err, rotated)
	}
	l.Set(nil)
	if l.Peer() != nil {
		t.Error("Set(nil) kept the peer")
	}
}
