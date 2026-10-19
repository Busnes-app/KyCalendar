// Package feed mirrors published ICS links into read-only calendars.
package feed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	MaxBody      = 10 << 20
	MaxObjects   = 5000
	MaxPerUser   = 10
	Interval     = 15 * time.Minute
	maxURL       = 2048
	maxRedirects = 3
	urlLabel     = "kycalendar:calendar-subscription-url"
	// maxParsed caps the objects one feed may produce. Time zones are copied into every object
	// that names them, so the output can far outgrow the 10 MiB download.
	maxParsed = 32 << 20
)

// maxParseTime bounds one feed's parse, as maxExpandTime bounds one event listing. Real feeds
// take milliseconds. A variable only so tests can shorten it.
var maxParseTime = 5 * time.Second

// userError is a failure shown to the user as is. It never carries the response body.
type userError string

func (e userError) Error() string { return string(e) }

var (
	ErrLink        = userError("the link must be an https:// or webcal:// address")
	errGone        = userError("the link no longer works; publish the calendar again and update the link")
	errStatus      = userError("the server refused the link")
	errNotCalendar = userError("the link did not return a calendar")
	errTooLarge    = userError("the calendar is too large")
	errTooMany     = userError("the calendar has more than 5000 events")
	errRedirects   = userError("the link redirected too many times")
	errRefused     = userError("the link points at a private or local address, which is refused")
	errStorage     = userError("calendar storage is full")
	errUnreachable = userError("could not reach the link")
)

// Message is the text the user sees for err.
func Message(err error) string {
	var ue userError
	switch {
	case errors.As(err, &ue):
		return string(ue)
	case errors.Is(err, sso.ErrAddressRefused):
		return string(errRefused)
	case errors.Is(err, store.ErrQuotaExceeded):
		return string(errStorage)
	}
	return string(errUnreachable)
}

// NormalizeURL checks a pasted link and returns it as https, with its host for display.
func NormalizeURL(raw string) (link, host string, err error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxURL {
		return "", "", ErrLink
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", ErrLink
	}
	if strings.EqualFold(u.Scheme, "webcal") {
		u.Scheme = "https"
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		return "", "", ErrLink
	}
	u.Scheme, u.Fragment = "https", ""
	return u.String(), strings.ToLower(u.Hostname()), nil
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// RefusePrivate admits public addresses only. A link comes from an everyday user, so the LAN
// (RFC 1918, IPv6 ULA, CGNAT) is refused as well as everything sso.RefuseLocal refuses.
func RefusePrivate(a netip.Addr) error {
	a = a.Unmap()
	if err := sso.RefuseLocal(a); err != nil {
		return err
	}
	if a.IsPrivate() || cgnat.Contains(a) {
		return sso.ErrAddressRefused
	}
	return nil
}

// NewClient fetches links: HTTPS on every hop, policy applied to each address dialled, up to
// maxRedirects redirects, bodies capped one byte over MaxBody so an oversized one is detected.
func NewClient(policy sso.AddrPolicy, roots *x509.CertPool) *http.Client {
	c := sso.NewGuardedClientLimit(policy, roots, MaxBody+1)
	c.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errRedirects
		}
		return nil
	}
	return c
}

// Seal encrypts a link under a key derived for links alone.
func Seal(master []byte, link string) (string, error) {
	return crypto.EncryptAESGCM([]byte(link), crypto.DeriveKey(master, urlLabel))
}

// Open reverses Seal.
func Open(master []byte, sealed string) (string, error) {
	b, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(master, urlLabel))
	return string(b), err
}

// Fetched is one response: Body is nil when the server answered 304 Not Modified.
type Fetched struct {
	Body               []byte
	ETag, LastModified string
}

// Fetch GETs link, conditional on the validators of the last response.
func Fetch(ctx context.Context, client *http.Client, link, etag, lastModified string) (*Fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, ErrLink
	}
	req.Header.Set("Accept", "text/calendar")
	req.Header.Set("User-Agent", "KyCalendar")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return &Fetched{ETag: etag, LastModified: lastModified}, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, errGone
	case resp.StatusCode != http.StatusOK:
		return nil, errStatus
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBody {
		return nil, errTooLarge
	}
	return &Fetched{Body: body, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
}

// Parse splits a feed into one calendar object per UID: the series master with its overrides
// and the time zones they name. Alarms are dropped, so a work calendar never rings the phone.
// Objects that fail the CalDAV write checks are skipped and counted. Each object's recurrence
// check (calendar.Inspect) may cost up to its own budget, so the feed gets maxParseTime in all;
// ctx is checked between objects, so a caller's timeout or a dropped request stops it too.
func Parse(ctx context.Context, calendarID string, body []byte) (objs []*store.CalendarObject, skipped int, err error) {
	// go-ical panics on some malformed parameters; refreshes run outside any HTTP recovery, so a
	// panic here would take the server down.
	defer func() {
		if recover() != nil {
			objs, skipped, err = nil, 0, errNotCalendar
		}
	}()
	if calendar.LongParams(body) {
		return nil, 0, errNotCalendar
	}
	src, err := ical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil || src.Name != ical.CompCalendar {
		return nil, 0, errNotCalendar
	}
	zones := map[string]*ical.Component{}
	byUID := map[string][]*ical.Component{}
	var order []string
	for _, comp := range src.Children {
		switch comp.Name {
		case ical.CompTimezone:
			if id, _ := comp.Props.Text(ical.PropTimezoneID); id != "" {
				zones[id] = comp
			}
		case ical.CompEvent:
			uid, _ := comp.Props.Text(ical.PropUID)
			if uid == "" {
				skipped++
				continue
			}
			if _, ok := byUID[uid]; !ok {
				order = append(order, uid)
			}
			byUID[uid] = append(byUID[uid], comp)
		}
	}
	if len(order) > MaxObjects {
		return nil, 0, errTooMany
	}
	// Every byte the encoder produces is charged, kept or not: a time zone is copied into each
	// object that names it, so skipped objects can cost as much as kept ones.
	budget := maxParsed
	deadline, cancel := context.WithTimeout(ctx, maxParseTime)
	defer cancel()
	for _, uid := range order {
		if deadline.Err() != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err() // the caller gave up
			}
			return nil, 0, errTooLarge // the feed's time ran out
		}
		limit := min(budget, calendar.MaxObjectSize)
		o, tried, err := object(calendarID, uid, byUID[uid], zones, limit)
		budget -= min(tried, budget)
		switch {
		case errors.Is(err, errTooLarge) && limit < calendar.MaxObjectSize:
			return nil, 0, errTooLarge // the feed's budget ran out, not just this object's
		case err != nil:
			skipped++
			continue
		}
		objs = append(objs, o)
	}
	return objs, skipped, nil
}

// capped is an encoding buffer that refuses to grow past max and counts every byte offered.
type capped struct {
	bytes.Buffer
	max, tried int
}

func (c *capped) Write(p []byte) (int, error) {
	c.tried += len(p)
	if c.Len()+len(p) > c.max {
		return 0, errTooLarge
	}
	return c.Buffer.Write(p)
}

// object builds uid's calendar object, encoding at most limit bytes (errTooLarge past it), and
// reports how many bytes the encoder offered.
func object(calendarID, uid string, events []*ical.Component, zones map[string]*ical.Component, limit int) (*store.CalendarObject, int, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//KyCalendar//Subscription//EN")
	named := map[string]bool{}
	for _, ev := range events {
		for _, props := range ev.Props {
			for _, p := range props {
				if id := p.Params.Get(ical.ParamTimezoneID); id != "" {
					named[id] = true
				}
			}
		}
	}
	// Sorted, and DTSTAMP pinned below: an unchanged event must encode to the same bytes, or every
	// refresh rewrites every event and every device re-downloads the calendar.
	for _, id := range slices.Sorted(maps.Keys(named)) {
		if z, ok := zones[id]; ok {
			cal.Children = append(cal.Children, z)
		}
	}
	for _, ev := range events {
		kept := ev.Children[:0:0]
		for _, child := range ev.Children {
			if child.Name != ical.CompAlarm {
				kept = append(kept, child)
			}
		}
		// Exchange stamps DTSTAMP with the time of each fetch; LAST-MODIFIED moves only on an edit.
		stamp := "19700101T000000Z"
		if lm := ev.Props.Get(ical.PropLastModified); lm != nil {
			stamp = lm.Value
		}
		ev.Props.Set(&ical.Prop{Name: ical.PropDateTimeStamp, Params: ical.Params{}, Value: stamp})
		cal.Children = append(cal.Children, &ical.Component{Name: ev.Name, Props: ev.Props, Children: kept})
	}
	buf := &capped{max: limit}
	if err := ical.NewEncoder(buf).Encode(cal); err != nil {
		return nil, buf.tried, err
	}
	sum := sha256.Sum256([]byte(uid))
	o, err := davbackend.Prepare(calendarID, hex.EncodeToString(sum[:16])+".ics", cal, buf.Bytes())
	return o, buf.tried, err
}
