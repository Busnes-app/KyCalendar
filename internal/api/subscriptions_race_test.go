package api_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/feed"
)

// Refresh now is one fetch per minute, however many requests arrive together: the slot is
// claimed in the store before the fetch, not checked against a value read earlier.
func TestSubscriptionRefreshIsClaimedOnce(t *testing.T) {
	w := newMCPWorld(t)
	body, err := os.ReadFile("../feed/testdata/m365.ics")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(300 * time.Millisecond) // a slow feed keeps the window open
		_, _ = rw.Write(body)
	}))
	defer ts.Close()
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	api.SetFeedClientForTest(w.srv, feed.NewClient(func(netip.Addr) error { return nil }, pool))

	var cal subCalendar
	if r := doJSON(t, w.srv, "POST", "/api/subscriptions", w.alice, map[string]string{"url": ts.URL + "/cal.ics", "name": "Work"}); r.Code != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", r.Code, r.Body.String())
	} else if err := json.Unmarshal(r.Body.Bytes(), &cal); err != nil {
		t.Fatal(err)
	}
	// Last checked two minutes ago: one refresh is due.
	ctx := context.Background()
	sub, err := w.st.Calendars().GetSubscription(ctx, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	sub.CheckedAt = &old
	if err := w.st.Calendars().RecordSubscriptionCheck(ctx, sub); err != nil {
		t.Fatal(err)
	}
	hits.Store(0)

	codes := make(chan int, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call(t, w.srv, "POST", "/api/calendars/"+cal.ID+"/refresh", "", w.alice).Code
		}()
	}
	wg.Wait()
	close(codes)
	count := map[int]int{}
	for c := range codes {
		count[c]++
	}
	if hits.Load() != 1 || count[http.StatusOK] != 1 || count[http.StatusTooManyRequests] != 19 {
		t.Fatalf("20 parallel refreshes: %d fetches, responses %v; want 1 fetch, one 200 and nineteen 429", hits.Load(), count)
	}
}
