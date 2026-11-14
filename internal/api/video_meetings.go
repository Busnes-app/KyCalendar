package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Video meetings: the store marks a meeting due in every write of its event (store
// video_meetings.go); SyncMeetings brings KyMessages up to date, one meeting at a time, so a
// close always follows the PUTs before it and nothing a user saves waits on KyMessages.
const (
	meetingBatch   = 20
	meetingBackoff = time.Minute
	meetingMaxWait = time.Hour
)

// meetingNow is the worker's clock; tests move it past a backoff.
var meetingNow = time.Now

var errPeopleUnavailable = errors.New("suite people lookup unavailable")

// resolvePeople turns attendee emails into KyIdentity subjects (suite contract A3, Resolve);
// unmatched addresses are absent. A variable so tests can stand in for KyIdentity.
var resolvePeople = func(ctx context.Context, dir *suitedir.Client, sub string, emails []string) ([]string, error) {
	people, err := dir.Resolve(ctx, sub, emails)
	subs := make([]string, 0, len(people))
	for _, p := range people {
		subs = append(subs, p.Sub)
	}
	return subs, err
}

// newMeetingID is a fresh opaque room ID, never derived from the event: 32 base64url characters.
func newMeetingID() string { return crypto.RandomBase64URL(24) }

// meetingsFor reports whether u can have video meetings: KyMessages is linked and u signed in
// through the live KyIdentity provider, so it has a subject KyMessages knows.
func (s *Server) meetingsFor(u *store.User) bool {
	p := s.signin.Load()
	return s.kymessages.Peer() != nil && u.Role == "user" && u.SSOSubject != "" && p != nil &&
		p.Kind == sso.KindKyIdentity && u.SSOIssuer == p.Binding && slices.Contains(sso.AccountProviders(p.Kind), u.SSOProvider)
}

// SyncMeetings sends due meetings to KyMessages until none is due. cmd/server calls it on a ticker.
func (s *Server) SyncMeetings(ctx context.Context) {
	for ctx.Err() == nil {
		due, err := s.store.Calendars().DueMeetings(ctx, meetingNow(), meetingBatch)
		if err != nil {
			log.Printf("[KYMESSAGES] listing due meetings failed: %v", err)
			return
		}
		for _, m := range due {
			if err := s.syncMeeting(ctx, m); err != nil {
				log.Printf("[KYMESSAGES] meeting %s: recording the result failed: %v", m.ID, err)
				return
			}
		}
		if len(due) < meetingBatch {
			return
		}
	}
}

// syncMeeting makes one attempt for m and records it; only a store failure is returned.
func (s *Server) syncMeeting(ctx context.Context, m *store.VideoMeeting) error {
	owner, err := s.store.Users().GetUserByID(ctx, m.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var who kymessages.User
	if owner != nil {
		who = kymessages.User{Sub: owner.SSOSubject, Issuer: owner.SSOIssuer}
	}
	if m.State == "closing" {
		return s.closeMeeting(ctx, m, who)
	}
	if owner == nil || owner.Status != "active" || owner.Role != "user" || owner.SSOSubject == "" {
		return s.refuseMeeting(ctx, m, "no_account")
	}
	c, o, cal, err := s.meetingEvent(ctx, m)
	if err != nil {
		return err
	}
	if cal == nil || !s.drivesMeeting(ctx, owner, cal) {
		return s.closeMeeting(ctx, m, who)
	}
	master, _ := calendar.Master(cal)
	title, _ := master.Props.Text(ical.PropSummary)
	req := kymessages.PutRequest{User: who, Title: kymessages.Title(title), MemberSubs: []string{}}
	resolved := true
	if emails := meetingEmails(cal, owner.Email); len(emails) > 0 {
		subs, err := s.meetingMembers(ctx, owner, emails)
		if err != nil {
			resolved = false
		} else {
			req.MemberSubs = subs
		}
	}
	body, _ := json.Marshal(req)
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	url := m.MemberURL
	// A failed lookup never shrinks a room that exists: a PUT removes members it does not list.
	// A new room is still created, organizer first, so the event gets its link.
	if url == "" || resolved && digest != m.Synced {
		res, err := s.kymessages.Put(ctx, m.ID, "evt_"+crypto.RandomHex(16), req)
		if err != nil {
			if err := s.meetingFailed(ctx, m, err, "put"); err != nil {
				return err
			}
			if refused := (kymessages.Refused{}); errors.As(err, &refused) && refused.Code == "closed" {
				s.writeMeetingLink(ctx, owner, c, o, cal, "") // a closed room's Join leads nowhere
			}
			return nil
		}
		url = res.MemberURL
		if len(res.SkippedSubs) > 0 {
			log.Printf("[KYMESSAGES] meeting %s: %d members have no KyMessages account yet", m.ID, len(res.SkippedSubs))
		}
		s.auditMeeting(ctx, m, "meeting.put", "")
	}
	if !resolved {
		// Keep the link, stay due: the people lookup is retried with backoff.
		if err := s.store.Calendars().SyncedMeeting(ctx, m.ID, m.DoneRev, m.Synced, url); err != nil {
			return err
		}
		s.writeMeetingLink(ctx, owner, c, o, cal, url)
		return s.store.Calendars().RetryMeeting(ctx, m.ID, meetingNow().Add(meetingWait(m.Attempts)), "people_unavailable")
	}
	if err := s.store.Calendars().SyncedMeeting(ctx, m.ID, m.Rev, digest, url); err != nil {
		return err
	}
	s.writeMeetingLink(ctx, owner, c, o, cal, url)
	return nil
}

// meetingEvent loads m's event; cal is nil when it is gone.
func (s *Server) meetingEvent(ctx context.Context, m *store.VideoMeeting) (*store.Calendar, *store.CalendarObject, *ical.Calendar, error) {
	c, err := s.store.Calendars().GetCalendarByID(ctx, m.CalendarID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil
	} else if err != nil {
		return nil, nil, nil, err
	}
	o, err := s.store.Calendars().GetObject(ctx, m.CalendarID, m.Name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil
	} else if err != nil {
		return nil, nil, nil, err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil || o.Component != ical.CompEvent {
		return nil, nil, nil, nil
	}
	return c, o, cal, nil
}

// drivesMeeting: only the organizer's live copy drives a room. An event cancelled, or one whose
// organizer is now somebody else, closes it.
func (s *Server) drivesMeeting(ctx context.Context, owner *store.User, cal *ical.Calendar) bool {
	master, err := calendar.Master(cal)
	if err != nil || master == nil {
		return false
	}
	if st, _ := master.Props.Text(ical.PropStatus); strings.EqualFold(st, "CANCELLED") {
		return false
	}
	org, ok := itip.Organizer(cal)
	if !ok {
		return true
	}
	self, ok, err := davbackend.Address(ctx, s.store, owner)
	return err == nil && ok && org == self
}

// meetingEmails are the attendees of every VEVENT but the organizer, each once, at most
// kymessages.MaxMembers.
func meetingEmails(cal *ical.Calendar, self string) []string {
	var out []string
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		for _, a := range itip.AttendeesOf(ev) {
			e := a.Address.Email()
			if !strings.EqualFold(e, self) && !slices.Contains(out, e) && len(out) < kymessages.MaxMembers {
				out = append(out, e)
			}
		}
	}
	return out
}

func (s *Server) meetingMembers(ctx context.Context, owner *store.User, emails []string) ([]string, error) {
	p := s.signin.Load()
	if p == nil || p.Kind != sso.KindKyIdentity {
		return nil, errPeopleUnavailable
	}
	dir, err := p.SuiteDirectory()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	subs, err := resolvePeople(ctx, dir, owner.SSOSubject, emails)
	if err != nil {
		return nil, err
	}
	subs = slices.DeleteFunc(subs, func(sub string) bool { return sub == owner.SSOSubject })
	return subs[:min(len(subs), kymessages.MaxMembers)], nil
}

// writeMeetingLink puts url into the organizer's event through the scheduled write path, so
// invitations sent from now on carry it. A conflicting edit makes the meeting due again anyway.
func (s *Server) writeMeetingLink(ctx context.Context, owner *store.User, c *store.Calendar, o *store.CalendarObject, cal *ical.Calendar, url string) {
	if master, _ := calendar.Master(cal); master != nil && calendar.Conference(master) == url {
		return
	}
	calendar.SetConference(cal, url)
	if _, apiErr := s.saveEvent(ctx, owner, c, o.Name, cal, o.ETag, false); apiErr != nil && apiErr != errEventChanged {
		log.Printf("[KYMESSAGES] meeting %s: writing the link into its event failed: %s", o.Name, apiErr.msg)
	}
}

func (s *Server) closeMeeting(ctx context.Context, m *store.VideoMeeting, who kymessages.User) error {
	if err := s.kymessages.Close(ctx, m.ID, "evt_"+crypto.RandomHex(16), who); err != nil {
		return s.meetingFailed(ctx, m, err, "close")
	}
	s.auditMeeting(ctx, m, "meeting.close", "")
	return s.store.Calendars().ClosedMeeting(ctx, m.ID)
}

// meetingFailed records err: a retry with backoff, or a refusal that stays until the event
// changes. A PUT refused as closed means KyMessages closed the room: so is the meeting.
func (s *Server) meetingFailed(ctx context.Context, m *store.VideoMeeting, err error, op string) error {
	var refused kymessages.Refused
	switch {
	case errors.As(err, &refused) && refused.Code == "closed":
		s.auditMeeting(ctx, m, "meeting.refused", "op="+op+" code=closed")
		return s.store.Calendars().ClosedMeeting(ctx, m.ID)
	case errors.As(err, &refused):
		s.auditMeeting(ctx, m, "meeting.refused", "op="+op+" code="+refused.Code)
		return s.refuseMeeting(ctx, m, refused.Code)
	}
	code := "unavailable"
	if errors.Is(err, kymessages.ErrNotConnected) {
		code = "not_connected"
	}
	return s.store.Calendars().RetryMeeting(ctx, m.ID, meetingNow().Add(meetingWait(m.Attempts)), code)
}

func (s *Server) refuseMeeting(ctx context.Context, m *store.VideoMeeting, code string) error {
	log.Printf("[KYMESSAGES] meeting %s refused: %s", m.ID, code)
	return s.store.Calendars().FailedMeeting(ctx, m.ID, m.Rev, code)
}

func meetingWait(attempts int) time.Duration {
	return min(meetingBackoff<<min(attempts, 10), meetingMaxWait)
}

// auditMeeting records an outcome with IDs only: never a title, an address or the link.
func (s *Server) auditMeeting(ctx context.Context, m *store.VideoMeeting, action, details string) {
	_ = s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: m.UserID, Action: action, Resource: "meeting:" + m.ID, Details: details})
}

var (
	errMeetingsOff         = &apiError{status: http.StatusUnprocessableEntity, msg: "Video meetings need KyMessages and a KyIdentity sign-in", code: "meetings_off"}
	errNotMeetingOrganizer = &apiError{status: http.StatusForbidden, msg: "Only the organizer can change the video meeting", code: "not_organizer"}
)

// meetingSwitch checks a web edit's video meeting switch for event o (nil: a new one) and
// takes a room link that is ending out of cal. nil, nil: no change.
func (s *Server) meetingSwitch(ctx context.Context, c *store.Calendar, o *store.CalendarObject, cal *ical.Calendar, want *bool) (*store.MeetingSwitch, *apiError) {
	if want == nil {
		return nil, nil
	}
	u := sessionUser(ctx)
	if org, ok := itip.Organizer(cal); ok {
		self, ok, err := davbackend.Address(ctx, s.store, u)
		if err != nil {
			return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to look up your address"}
		}
		if !ok || org != self {
			return nil, errNotMeetingOrganizer
		}
	}
	var open bool
	if o != nil {
		_, err := s.store.Calendars().OpenMeeting(ctx, c.ID, o.Name)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to load the event"}
		}
		open = err == nil
	}
	switch {
	case !*want:
		calendar.SetConference(cal, "")
		if !open {
			return nil, nil
		}
		return &store.MeetingSwitch{}, nil
	case open:
		return nil, nil
	case !s.meetingsFor(u):
		return nil, errMeetingsOff
	}
	calendar.SetConference(cal, "") // a closed room's link never carries over
	return &store.MeetingSwitch{Open: true, NewID: newMeetingID()}, nil
}
