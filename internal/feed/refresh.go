package feed

import (
	"context"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// Refresher mirrors subscriptions into their calendars.
type Refresher struct {
	Store  store.Store
	Key    []byte // the deployment encryption key; links are sealed under a key derived from it
	Client *http.Client
	Limits store.OwnerLimits
}

// slots caps fetches and parses running at once across the whole server (routes and the refresh
// loop alike): each holds up to MaxBody of download and maxParsed of objects.
var slots = make(chan struct{}, 4)

// backoff is the wait after the nth failure in a row.
func backoff(n int) time.Duration {
	switch n {
	case 1:
		return Interval
	case 2:
		return time.Hour
	}
	return 6 * time.Hour
}

// Check fetches sub's link once and mirrors it. The outcome is recorded on sub and in the
// store whether it succeeds or not; the error returned is the failure, already recorded.
func (r *Refresher) Check(ctx context.Context, sub *store.Subscription, now time.Time) error {
	link, err := Open(r.Key, sub.URLSealed)
	if err == nil {
		err = r.mirror(ctx, sub, link)
	}
	sub.CheckedAt = &now
	if err != nil {
		sub.Failures++
		sub.Error = Message(err)
		sub.NextCheckAt = now.Add(backoff(sub.Failures))
	} else {
		sub.Failures, sub.Error, sub.RefreshedAt = 0, "", &now
		// Jitter spreads many subscriptions to one host over the interval.
		sub.NextCheckAt = now.Add(Interval + rand.N(2*time.Minute))
	}
	if rerr := r.Store.Calendars().RecordSubscriptionCheck(ctx, sub); rerr != nil && err == nil {
		return rerr
	}
	return err
}

// Load fetches link and parses it into calendarID's objects. objs is nil when the server
// answered 304 Not Modified.
func (r *Refresher) Load(ctx context.Context, calendarID, host, link, etag, lastModified string) (*Fetched, []*store.CalendarObject, error) {
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	f, err := Fetch(ctx, r.Client, link, etag, lastModified)
	if err != nil || f.Body == nil {
		return f, nil, err
	}
	objs, skipped, err := Parse(ctx, calendarID, f.Body)
	if err != nil {
		return nil, nil, err
	}
	if skipped > 0 {
		log.Printf("[FEED] %s (%s): skipped %d events that are not valid calendar objects", calendarID, strings.NewReplacer("\n", "", "\r", "").Replace(host), skipped)
	}
	return f, objs, nil
}

func (r *Refresher) mirror(ctx context.Context, sub *store.Subscription, link string) error {
	f, objs, err := r.Load(ctx, sub.CalendarID, sub.URLHost, link, sub.ETag, sub.LastModified)
	if err != nil {
		return err
	}
	if f.Body == nil {
		return nil
	}
	if err := r.Store.Calendars().ReplaceSubscriptionObjects(ctx, sub.CalendarID, objs, r.Limits); err != nil {
		return err
	}
	sub.ETag, sub.LastModified = f.ETag, f.LastModified
	return nil
}

// RunDue checks every subscription that is due, a batch at a time, one after another.
func (r *Refresher) RunDue(ctx context.Context, now time.Time) {
	subs, err := r.Store.Calendars().DueSubscriptions(ctx, now, 20)
	if err != nil {
		log.Printf("[FEED] listing due subscriptions failed: %v", err)
		return
	}
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		if err := r.Check(cctx, sub, now); err != nil {
			// Host only: the path and query of a published link are its secret.
			log.Printf("[FEED] %s (%s): %s", sub.CalendarID, sub.URLHost, Message(err))
		}
		cancel()
	}
}
