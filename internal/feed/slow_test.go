package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowFeed is MaxObjects small events, each with an hourly rule whose expansion uses up
// Inspect's per-object budget: about 8 ms each, about 40 s in all, and well under every byte cap.
func slowFeed() string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\n")
	for i := range MaxObjects {
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:%d\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260101T090000Z\r\nRRULE:FREQ=HOURLY;UNTIL=20380101T000000Z\r\nEND:VEVENT\r\n", i)
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func TestParseStopsAtItsDeadline(t *testing.T) {
	defer func(d time.Duration) { maxParseTime = d }(maxParseTime)
	maxParseTime = time.Second
	start := time.Now()
	_, _, err := Parse(context.Background(), "c", []byte(slowFeed()))
	if Message(err) != string(errTooLarge) || time.Since(start) > 2*time.Second {
		t.Fatalf("recurrence-heavy feed: %v after %v; want too large within about a second", err, time.Since(start))
	}
}

// A caller's timeout stops the parse too, so a fetch slot is never held past it.
func TestLoadHonoursItsContext(t *testing.T) {
	body := slowFeed()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer ts.Close()
	r := &Refresher{Client: tlsClient(ts, allowAll)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := r.Load(ctx, "c", "h", ts.URL+"/cal.ics", "", "")
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("Load with a 1 s context: %v after %v; want an error within about a second", err, time.Since(start))
	}
}
