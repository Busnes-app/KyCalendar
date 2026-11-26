package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/booking"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// bookingAllowedSetting is the administrator switch "Allow booking pages"; absent is off.
const bookingAllowedSetting = "booking_pages_allowed"

// Why booking pages are unavailable, server-wide then for one member.
const (
	bookingOff        = "off"
	bookingNoSched    = "scheduling_off"
	bookingNoKyPost   = "kypost_unpaired"
	bookingNoAddress  = "no_address"
	bookingNoIdentity = "no_kypost_account"
	bookingOrgOff     = "org_off"
)

func (s *Server) bookingAllowed(ctx context.Context) bool {
	v, err := s.store.Settings().GetSetting(ctx, bookingAllowedSetting)
	return err == nil && v == "true"
}

// bookingPrereq is why the server cannot run booking pages whatever the switch says, or "".
func (s *Server) bookingPrereq() string {
	switch {
	case s.sched == nil:
		return bookingNoSched
	case s.kypost.Peer() == nil:
		return bookingNoKyPost
	}
	return ""
}

// bookingReason is why booking pages are unavailable to u (nil: server-wide), or "".
func (s *Server) bookingReason(ctx context.Context, u *store.User) (string, error) {
	if !s.bookingAllowed(ctx) {
		return bookingOff, nil
	}
	if r := s.bookingPrereq(); r != "" {
		return r, nil
	}
	if u == nil {
		return "", nil
	}
	if q, err := s.store.OrgQuotas(ctx, u.OrgID); err != nil {
		return "", err
	} else if q.BookingPages != nil && !*q.BookingPages {
		return bookingOrgOff, nil
	}
	if _, ok, err := davbackend.Address(ctx, s.store, u); err != nil || !ok {
		if err != nil {
			return "", err
		}
		return bookingNoAddress, nil
	}
	if _, ok := davbackend.KyPostUser(u); !ok {
		return bookingNoIdentity, nil
	}
	return "", nil
}

// ---- Administrator switch ----

func (s *Server) handleGetBookingAdmin(w http.ResponseWriter, r *http.Request) {
	reason, _ := s.bookingReason(r.Context(), nil)
	s.writeJSON(w, http.StatusOK, map[string]any{"allowed": s.bookingAllowed(r.Context()), "available": reason == "", "reason": reason, "prerequisite": s.bookingPrereq()})
}

func (s *Server) handleSetBookingAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.requireStepUp(w, r, "change booking pages") {
		return
	}
	var req struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Allowed == nil {
		s.writeError(w, http.StatusBadRequest, "Give allowed: true or false")
		return
	}
	if p := s.bookingPrereq(); *req.Allowed && p != "" {
		msg := "Booking pages need KyPost to mail invitations; pair KyPost first"
		if p == bookingNoSched {
			msg = "Booking pages need invitations (KYCALENDAR_SCHEDULING) turned on"
		}
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": msg, "code": p})
		return
	}
	ctx := context.WithoutCancel(r.Context())
	was := s.bookingAllowed(ctx)
	if err := s.store.Settings().SetSetting(ctx, bookingAllowedSetting, fmt.Sprint(*req.Allowed)); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the setting")
		return
	}
	if was != *req.Allowed {
		if err := s.store.Audit().LogAudit(ctx, &store.AuditRecord{UserID: sessionUser(r.Context()).ID, Action: "admin.booking_pages",
			Resource: "settings", Details: fmt.Sprintf("from=%t to=%t", was, *req.Allowed), IPAddress: s.requestIP(r)}); err != nil {
			log.Printf("[SECURITY] audit admin.booking_pages not recorded: %v", err)
		}
	}
	s.handleGetBookingAdmin(w, r)
}

// ---- Member booking types ----

type bookingTypeBody struct {
	Enabled         bool             `json:"enabled"`
	Title           string           `json:"title"`
	DurationMin     int              `json:"duration_min"`
	Zone            string           `json:"zone"`
	Windows         []booking.Window `json:"windows"`
	BufferBeforeMin int              `json:"buffer_before_min"`
	BufferAfterMin  int              `json:"buffer_after_min"`
	NoticeMin       *int             `json:"notice_min"`
	HorizonDays     *int             `json:"horizon_days"`
	DailyCap        int              `json:"daily_cap"`
	CalendarID      string           `json:"calendar_id"`
	MeetingRoom     bool             `json:"meeting_room"`
}

type bookingTypeView struct {
	ID              string           `json:"id"`
	Enabled         bool             `json:"enabled"`
	Slug            string           `json:"slug"`
	URL             string           `json:"url"`
	Title           string           `json:"title"`
	DurationMin     int              `json:"duration_min"`
	Zone            string           `json:"zone"`
	Windows         []booking.Window `json:"windows"`
	BufferBeforeMin int              `json:"buffer_before_min"`
	BufferAfterMin  int              `json:"buffer_after_min"`
	NoticeMin       int              `json:"notice_min"`
	HorizonDays     int              `json:"horizon_days"`
	DailyCap        int              `json:"daily_cap"`
	CalendarID      string           `json:"calendar_id"`
	MeetingRoom     bool             `json:"meeting_room"`
}

func (s *Server) appURL(path string) string {
	return strings.TrimRight(s.config.Server.AppURL, "/") + path
}

func (s *Server) bookingTypeView(t *store.BookingType) bookingTypeView {
	var ws []booking.Window
	_ = json.Unmarshal([]byte(t.Windows), &ws)
	return bookingTypeView{ID: t.ID, Enabled: t.Enabled, Slug: t.Slug, URL: s.appURL("/book/" + t.Slug), Title: t.Title,
		DurationMin: t.DurationMin, Zone: t.Zone, Windows: ws, BufferBeforeMin: t.BufferBeforeMin, BufferAfterMin: t.BufferAfterMin,
		NoticeMin: t.NoticeMin, HorizonDays: t.HorizonDays, DailyCap: t.DailyCap, CalendarID: t.CalendarID, MeetingRoom: t.MeetingRoom}
}

func between(v, lo, hi int) bool { return v >= lo && v <= hi }

// applyBookingType validates b at the boundary and writes it into t.
func (s *Server) applyBookingType(ctx context.Context, u *store.User, b bookingTypeBody, t *store.BookingType) *apiError {
	bad := func(msg string) *apiError {
		return &apiError{status: http.StatusBadRequest, msg: msg, code: "invalid"}
	}
	if b.MeetingRoom && !t.MeetingRoom && !s.meetingsFor(u) {
		return &apiError{status: http.StatusConflict, msg: "Video meetings are not available to you, so a booking cannot get a meeting room", code: "meetings_off"}
	}
	title, err := booking.CleanTitle(b.Title)
	if err != nil {
		return bad(strings.TrimPrefix(err.Error(), "booking: invalid: "))
	}
	notice, horizon := booking.DefaultNoticeMin, booking.DefaultHorizonDays
	if b.NoticeMin != nil {
		notice = *b.NoticeMin
	}
	if b.HorizonDays != nil {
		horizon = *b.HorizonDays
	}
	switch {
	case !between(b.DurationMin, 5, 480):
		return bad("The length must be 5 to 480 minutes")
	case !between(b.BufferBeforeMin, 0, 240) || !between(b.BufferAfterMin, 0, 240):
		return bad("Buffers must be 0 to 240 minutes")
	case !between(notice, 0, 43200):
		return bad("Minimum notice must be 0 to 43200 minutes (30 days)")
	case !between(horizon, 1, booking.MaxHorizonDays):
		return bad(fmt.Sprintf("Bookings may go 1 to %d days ahead", booking.MaxHorizonDays))
	case !between(b.DailyCap, 0, booking.MaxDailyCap):
		return bad(fmt.Sprintf("The daily limit must be 0 (none) to %d", booking.MaxDailyCap))
	}
	if loc, ok := zoneNamed(b.Zone); !ok || b.Zone == "" || loc == nil {
		return bad("Give an IANA time zone, such as Europe/London")
	}
	if err := booking.CheckWindows(b.Windows); err != nil {
		return bad(strings.TrimPrefix(err.Error(), "booking: invalid: "))
	}
	c, err := s.store.Calendars().GetCalendarByID(ctx, b.CalendarID)
	if err != nil || c.OwnerKind != "user" || c.OwnerID != u.ID || c.Subscribed || !c.Holds(ical.CompEvent) {
		return bad("Choose one of your own event calendars")
	}
	if b.Enabled {
		reason, err := s.bookingReason(ctx, u)
		if err != nil {
			return &apiError{status: http.StatusInternalServerError, msg: "Failed to check booking pages"}
		}
		if reason != "" {
			return &apiError{status: http.StatusConflict, msg: "Booking pages are not available to you: " + bookingWords(reason), code: reason}
		}
	}
	ws, _ := json.Marshal(b.Windows)
	t.Enabled, t.Title, t.DurationMin, t.Zone, t.Windows = b.Enabled, title, b.DurationMin, b.Zone, string(ws)
	t.BufferBeforeMin, t.BufferAfterMin, t.NoticeMin, t.HorizonDays, t.DailyCap, t.CalendarID = b.BufferBeforeMin, b.BufferAfterMin, notice, horizon, b.DailyCap, c.ID
	t.MeetingRoom = b.MeetingRoom
	return nil
}

// bookingWords says a reason code in words for the member.
func bookingWords(reason string) string {
	return map[string]string{
		bookingOff:        "your administrator has not allowed booking pages",
		bookingNoSched:    "invitations are turned off on this server",
		bookingNoKyPost:   "this server is not paired with KyPost, which mails the invitations",
		bookingNoAddress:  "your account has no email address of its own",
		bookingNoIdentity: "your account does not sign in through KyIdentity, so KyPost cannot mail for it",
		bookingOrgOff:     "your organization's plan does not include booking pages",
	}[reason]
}

func (s *Server) handleListBookingTypes(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r.Context())
	reason, err := s.bookingReason(r.Context(), u)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to check booking pages")
		return
	}
	list, err := s.store.Bookings().ListTypes(r.Context(), u.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list booking pages")
		return
	}
	views := []bookingTypeView{}
	for _, t := range list {
		views = append(views, s.bookingTypeView(t))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"available": reason == "", "reason": reason, "reason_text": bookingWords(reason), "meetings": s.meetingsFor(u), "types": views})
}

func decodeBookingType(w http.ResponseWriter, r *http.Request) (bookingTypeBody, bool) {
	var b bookingTypeBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	return b, dec.Decode(&b) == nil
}

func (s *Server) handleCreateBookingType(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r.Context())
	b, ok := decodeBookingType(w, r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	t := &store.BookingType{ID: uuid.NewString(), UserID: u.ID, Slug: crypto.RandomHex(16)}
	if apiErr := s.applyBookingType(r.Context(), u, b, t); apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	switch err := s.store.Bookings().CreateType(r.Context(), t, booking.MaxTypesPerUser); {
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("You can have at most %d booking pages", booking.MaxTypesPerUser), "code": "limit"})
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to save the booking page")
	default:
		s.writeJSON(w, http.StatusCreated, s.bookingTypeView(t))
	}
}

func (s *Server) ownBookingType(w http.ResponseWriter, r *http.Request) *store.BookingType {
	t, err := s.store.Bookings().GetType(r.Context(), sessionUser(r.Context()).ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such booking page")
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to load the booking page")
	default:
		return t
	}
	return nil
}

func (s *Server) saveBookingType(w http.ResponseWriter, r *http.Request, t *store.BookingType) {
	if err := s.store.Bookings().UpdateType(r.Context(), t); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save the booking page")
		return
	}
	s.writeJSON(w, http.StatusOK, s.bookingTypeView(t))
}

func (s *Server) handleUpdateBookingType(w http.ResponseWriter, r *http.Request) {
	t := s.ownBookingType(w, r)
	if t == nil {
		return
	}
	b, ok := decodeBookingType(w, r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if apiErr := s.applyBookingType(r.Context(), sessionUser(r.Context()), b, t); apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	s.saveBookingType(w, r, t)
}

// handleRotateBookingType gives the page a new address; the old one is dead at once.
func (s *Server) handleRotateBookingType(w http.ResponseWriter, r *http.Request) {
	if t := s.ownBookingType(w, r); t != nil {
		t.Slug = crypto.RandomHex(16)
		s.saveBookingType(w, r, t)
	}
}

func (s *Server) handleDeleteBookingType(w http.ResponseWriter, r *http.Request) {
	switch err := s.store.Bookings().DeleteType(r.Context(), sessionUser(r.Context()).ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "No such booking page")
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the booking page")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- Public pages ----

var errNoBookingPage = &apiError{status: http.StatusNotFound, msg: "This booking page does not exist or is turned off", code: "not_found"}

// errSlotTaken is a slot a check found taken, off the grid or past the day's cap.
var errSlotTaken = errors.New("booking: slot unavailable")

// bookingHost is a booking type that may take bookings now, with its owner and rules.
type bookingHost struct {
	t     *store.BookingType
	owner *store.User
	org   itip.Address
	rules booking.Rules
}

func rulesOf(t *store.BookingType) (booking.Rules, error) {
	loc, err := time.LoadLocation(t.Zone)
	if err != nil {
		return booking.Rules{}, err
	}
	var ws []booking.Window
	if err := json.Unmarshal([]byte(t.Windows), &ws); err != nil {
		return booking.Rules{}, err
	}
	m := time.Minute
	return booking.Rules{Duration: time.Duration(t.DurationMin) * m, BufferBefore: time.Duration(t.BufferBeforeMin) * m,
		BufferAfter: time.Duration(t.BufferAfterMin) * m, Notice: time.Duration(t.NoticeMin) * m,
		HorizonDays: t.HorizonDays, DailyCap: t.DailyCap, Zone: loc, Windows: ws}, nil
}

// hostOf is t ready to book: enabled, its owner an active everyday member to whom booking pages
// are available. Anything else is errNoBookingPage, so a visitor learns nothing.
func (s *Server) hostOf(ctx context.Context, t *store.BookingType) (*bookingHost, *apiError) {
	if !t.Enabled {
		return nil, errNoBookingPage
	}
	u, err := s.store.Users().GetUserByID(ctx, t.UserID)
	if err != nil || u.Role != "user" || u.Status != "active" || !u.OrgActive {
		return nil, errNoBookingPage
	}
	if reason, err := s.bookingReason(ctx, u); err != nil || reason != "" {
		return nil, errNoBookingPage
	}
	org, _, _ := davbackend.Address(ctx, s.store, u)
	rules, err := rulesOf(t)
	if err != nil {
		return nil, errNoBookingPage
	}
	return &bookingHost{t: t, owner: u, org: org, rules: rules}, nil
}

func validSlug(s string) bool {
	return len(s) == 32 && strings.Trim(s, "0123456789abcdef") == ""
}

// pageHost is the bookable type at the path's slug.
func (s *Server) pageHost(ctx context.Context, slug string) (*bookingHost, *apiError) {
	if reason, _ := s.bookingReason(ctx, nil); reason != "" || !validSlug(slug) {
		return nil, errNoBookingPage
	}
	t, err := s.store.Bookings().TypeBySlug(ctx, slug)
	if err != nil {
		return nil, errNoBookingPage
	}
	return s.hostOf(ctx, t)
}

// tally adds holds to busy and counts this type's holds by day.
func (h *bookingHost) tally(busy []booking.Interval, perDay map[string]int, holds []*store.Booking) []booking.Interval {
	for _, b := range holds {
		busy = append(busy, booking.Interval{Start: time.Unix(b.Start, 0), End: time.Unix(b.End, 0)})
		if b.TypeID == h.t.ID {
			perDay[h.rules.Day(time.Unix(b.Start, 0))]++
		}
	}
	return busy
}

// slots is what the page offers now; skip leaves one booking's own event out (reschedule).
func (s *Server) slots(ctx context.Context, h *bookingHost, now time.Time, skip *store.Booking) ([]booking.Interval, error) {
	span := h.rules.Range(now)
	var skipCal, skipName string
	if skip != nil {
		skipCal, skipName = skip.CalendarID, skip.ObjectName
	}
	objs, err := s.store.Bookings().OwnerEvents(ctx, h.owner.ID, span.Start.Unix(), span.End.Unix(), skipCal, skipName)
	if err != nil {
		return nil, err
	}
	busy, perDay, err := h.rules.Busy(objs, h.t.ID, span, now.Add(2*time.Second))
	if err != nil {
		return nil, err
	}
	holds, err := s.store.Bookings().OpenHolds(ctx, h.owner.ID, span.Start.Unix(), span.End.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	return h.rules.Slots(now, h.tally(busy, perDay, holds), perDay), nil
}

// checkSpan is what a booking transaction rereads for slot: the slot with buffers and its day.
func (h *bookingHost) checkSpan(slot booking.Interval) (int64, int64) {
	day := h.rules.DayBounds(slot.Start)
	from, to := min(day.Start.Unix(), slot.Start.Add(-h.rules.BufferBefore).Unix()), max(day.End.Unix(), slot.End.Add(h.rules.BufferAfter).Unix())
	return from, to
}

// offeredCheck is the in-transaction rule for a new hold or a move: start is on the grid now.
func (h *bookingHost) offeredCheck(now, start time.Time) store.BookingCheck {
	return func(objs [][]byte, holds []*store.Booking) error {
		slot := booking.Interval{Start: start, End: start.Add(h.rules.Duration)}
		from, to := h.checkSpan(slot)
		busy, perDay, err := h.rules.Busy(objs, h.t.ID, booking.Interval{Start: time.Unix(from, 0), End: time.Unix(to, 0)}, time.Time{})
		if err != nil {
			return err
		}
		if _, ok := h.rules.Offered(now, h.tally(busy, perDay, holds), perDay, start); !ok {
			return errSlotTaken
		}
		return nil
	}
}

// heldCheck is the in-transaction rule at confirmation: the held slot is still free and in the
// future, and the day still has room without this hold.
func (h *bookingHost) heldCheck(now time.Time, slot booking.Interval) store.BookingCheck {
	return func(objs [][]byte, holds []*store.Booking) error {
		from, to := h.checkSpan(slot)
		busy, perDay, err := h.rules.Busy(objs, h.t.ID, booking.Interval{Start: time.Unix(from, 0), End: time.Unix(to, 0)}, time.Time{})
		if err != nil {
			return err
		}
		busy = h.tally(busy, perDay, holds)
		if !slot.Start.After(now) || !h.rules.Free(busy, slot) || (h.rules.DailyCap > 0 && perDay[h.rules.Day(slot.Start)] >= h.rules.DailyCap) {
			return errSlotTaken
		}
		return nil
	}
}

type slotView struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func slotViews(slots []booking.Interval) []slotView {
	out := []slotView{}
	for _, s := range slots {
		out = append(out, slotView{s.Start.UTC().Format(time.RFC3339), s.End.UTC().Format(time.RFC3339)})
	}
	return out
}

func (s *Server) hostName(u *store.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// issueBookingCSRF hands a signed-out visitor the pre-session CSRF token the public forms need.
// A signed-in member keeps their session's token, which the forms accept too.
func (s *Server) issueBookingCSRF(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.sessions.AuthenticateRequest(r); err != nil {
		s.sessions.IssuePreSession(w)
	}
}

func (s *Server) limited(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many requests; wait a few minutes and try again", "code": "rate_limited"})
}

func (s *Server) handleBookingPage(w http.ResponseWriter, r *http.Request) {
	if !s.allowPerIP(r, "book-view", 60, time.Minute) {
		s.limited(w)
		return
	}
	h, apiErr := s.pageHost(r.Context(), r.PathValue("slug"))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	if !s.allowAccountAttempt(r.Context(), "book-view-type:"+h.t.ID, 600, time.Minute) {
		s.limited(w)
		return
	}
	slots, err := s.slots(r.Context(), h, time.Now(), nil)
	if err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Free times cannot be shown right now; try again later", "code": "unavailable"})
		return
	}
	s.issueBookingCSRF(w, r)
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]any{"host": s.hostName(h.owner), "title": h.t.Title, "duration_min": h.t.DurationMin, "slots": slotViews(slots)})
}

// parseEmail is a visitor's address at the boundary.
func parseEmail(e string) (itip.Address, bool) {
	a, ok := itip.ParseAddress("mailto:" + strings.TrimSpace(e))
	if !ok {
		return "", false
	}
	local, domain, found := strings.Cut(a.Email(), "@")
	return itip.Address("mailto:" + strings.ToLower(a.Email())), found && local != "" && strings.Contains(domain, ".") && !strings.ContainsRune(domain, '@')
}

func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

func (s *Server) handleBookingHold(w http.ResponseWriter, r *http.Request) {
	if !s.allowPerIP(r, "book-hold", 5, 15*time.Minute) {
		s.limited(w)
		return
	}
	ctx := r.Context()
	h, apiErr := s.pageHost(ctx, r.PathValue("slug"))
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	var req struct{ Start, Name, Email, Note string }
	if !decodeStrict(w, r, &req) {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name, err1 := booking.CleanName(req.Name)
	note, err2 := booking.CleanNote(req.Note)
	if err := errors.Join(err1, err2); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": strings.ReplaceAll(err.Error(), "booking: invalid: ", ""), "code": "invalid"})
		return
	}
	addr, ok := parseEmail(req.Email)
	if !ok || addr == h.org {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Give your own email address, such as name@example.com", "code": "invalid"})
		return
	}
	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a time", "code": "invalid"})
		return
	}
	if !s.allowAccountAttempt(r.Context(), "book-hold-type:"+h.t.ID, 20, time.Hour) {
		s.limited(w)
		return
	}
	now := time.Now()
	slot := booking.Interval{Start: start, End: start.Add(h.rules.Duration)}
	token := crypto.RandomHex(32)
	b := &store.Booking{ID: uuid.NewString(), TypeID: h.t.ID, UserID: h.owner.ID, Email: addr.Email(),
		EmailHash: crypto.SHA256Hex([]byte(addr.Email())), Name: name, Note: note, ConfirmHash: crypto.SHA256Hex([]byte(token)),
		UID: uuid.NewString(), Start: slot.Start.Unix(), End: slot.End.Unix(), ExpiresAt: now.Add(booking.HoldFor).Unix()}
	from, to := h.checkSpan(slot)
	switch err := s.store.Bookings().Hold(ctx, b, now.Unix(), from, to, booking.MaxHoldsPerMail, h.offeredCheck(now, start)); {
	case errors.Is(err, errSlotTaken):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "That time is no longer free; choose another", "code": "slot_unavailable"})
		return
	case errors.Is(err, store.ErrTooManyHolds):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "This address already has bookings waiting for confirmation; check your mail", "code": "too_many_holds"})
		return
	case err != nil:
		log.Printf("booking hold for type %s failed: %v", h.t.ID, err)
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The time cannot be held right now; try again later", "code": "unavailable"})
		return
	}
	expires := time.Unix(b.ExpiresAt, 0)
	msg := booking.HoldMessage(b.UID, h.t.Title, s.hostName(h.owner), h.org.Email(), addr.Email(), s.appURL("/book/c#"+b.ID+"."+token), slot, expires, now)
	if err := s.sendHold(ctx, h, b, msg); err != nil {
		_ = s.store.Bookings().DeleteHold(context.WithoutCancel(ctx), b.ID)
		log.Printf("booking hold %s: confirmation not mailed: %v", b.ID, err)
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "This booking page cannot send mail right now; try again later", "code": "mail_failed"})
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]string{"expires_at": expires.UTC().Format(time.RFC3339)})
}

// sendHold mails the hold message from the member's mailbox through the scheduler's KyPost
// sender, the one external invitations use.
func (s *Server) sendHold(ctx context.Context, h *bookingHost, b *store.Booking, msg *ical.Calendar) error {
	who, _ := davbackend.KyPostUser(h.owner)
	if s.sched.KyPost == nil {
		return errors.New("kypost unavailable")
	}
	org, err := store.PeerOrg(ctx, s.store, h.owner)
	if err != nil {
		return err
	}
	who.OrgID = org
	var ics strings.Builder
	if err := ical.NewEncoder(&ics).Encode(msg); err != nil {
		return err
	}
	err = s.sched.KyPost.Send(ctx, "booking-hold-"+b.ID, kypost.SendRequest{User: who, From: h.org.Email(), Recipients: []string{b.Email},
		Method: itip.MethodRequest, ICS: ics.String()})
	var refused kypost.Refused
	if errors.As(err, &refused) {
		return errors.New("kypost refused: " + refused.Code)
	}
	if err != nil {
		return errors.New("kypost unavailable")
	}
	return nil
}

// linkBody is what a confirm or manage page posts: the booking ID and token from its fragment.
type linkBody struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Start string `json:"start,omitempty"`
}

var errBadLink = &apiError{status: http.StatusNotFound, msg: "This link is not valid, or the booking no longer exists", code: "not_found"}

// linked is the booking and its bookable host for a link, when the token matches hash(b).
func (s *Server) linked(w http.ResponseWriter, r *http.Request, hash func(*store.Booking) string) (*linkBody, *store.Booking, *bookingHost, bool) {
	if !s.allowPerIP(r, "book-link", 30, 15*time.Minute) {
		s.limited(w)
		return nil, nil, nil, false
	}
	var req linkBody
	if !decodeStrict(w, r, &req) {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return nil, nil, nil, false
	}
	ctx := r.Context()
	if reason, _ := s.bookingReason(ctx, nil); reason != "" || len(req.ID) > 64 || len(req.Token) != 64 {
		s.writeAPIError(w, errBadLink)
		return nil, nil, nil, false
	}
	b, err := s.store.Bookings().Get(ctx, req.ID)
	if err != nil || subtle.ConstantTimeCompare([]byte(crypto.SHA256Hex([]byte(req.Token))), []byte(hash(b))) != 1 {
		s.writeAPIError(w, errBadLink)
		return nil, nil, nil, false
	}
	t, err := s.store.Bookings().GetType(ctx, b.UserID, b.TypeID)
	if err != nil {
		s.writeAPIError(w, errBadLink)
		return nil, nil, nil, false
	}
	h, apiErr := s.hostOf(ctx, t)
	if apiErr != nil {
		s.writeAPIError(w, errBadLink)
		return nil, nil, nil, false
	}
	return &req, b, h, true
}

func confirmHash(b *store.Booking) string { return b.ConfirmHash }

func manageHash(b *store.Booking) string {
	if b.Status != store.BookingConfirmed {
		return ""
	}
	return b.ManageHash
}

func (s *Server) bookingView(h *bookingHost, b *store.Booking, status string) map[string]any {
	return map[string]any{"status": status, "host": s.hostName(h.owner), "title": h.t.Title,
		"start": time.Unix(b.Start, 0).UTC().Format(time.RFC3339), "end": time.Unix(b.End, 0).UTC().Format(time.RFC3339)}
}

func holdStatus(b *store.Booking, now time.Time) string {
	switch {
	case b.Status == store.BookingConfirmed:
		return "confirmed"
	case b.ExpiresAt <= now.Unix():
		return "expired"
	}
	return "held"
}

func (s *Server) handleBookingConfirmView(w http.ResponseWriter, r *http.Request) {
	if _, b, h, ok := s.linked(w, r, confirmHash); ok {
		s.writeJSON(w, http.StatusOK, s.bookingView(h, b, holdStatus(b, time.Now())))
	}
}

func (s *Server) handleBookingConfirm(w http.ResponseWriter, r *http.Request) {
	_, b, h, ok := s.linked(w, r, confirmHash)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context()) // the event, its mail and the row land together
	now := time.Now()
	slot := booking.Interval{Start: time.Unix(b.Start, 0), End: time.Unix(b.End, 0)}
	token := crypto.RandomHex(32)
	cal := booking.Booked(b.UID, h.t.ID, h.t.Title, b.Name, b.Note, s.appURL("/book/m#"+b.ID+"."+token), h.rules.Zone, slot, now)
	if err := itip.SetAttendees(cal, h.org, []itip.Address{itip.Address("mailto:" + b.Email)}); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to build the booking")
		return
	}
	booking.NameAttendee(cal, b.Email, b.Name)
	o, apiErr := s.bookingObject(ctx, h, b.UID+".ics", cal)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	// The room opens through the meetings outbox like any event's; the booker, outside the
	// organisation, joins only through their own guest link (Scheduler.GuestLink).
	if h.t.MeetingRoom && s.meetingsFor(h.owner) {
		o.Meeting = &store.MeetingSwitch{Open: true, NewID: newMeetingID()}
	}
	from, to := h.checkSpan(slot)
	manageHash := crypto.SHA256Hex([]byte(token))
	plan := s.sched.Plan(ctx, h.owner, cal)
	err := store.RetryStorageRefresh(ctx, func() error {
		attempt := *o
		return s.store.Bookings().Confirm(ctx, b.ID, now.Unix(), from, to, h.heldCheck(now, slot), &attempt, manageHash, s.objectLimits(), plan)
	})
	if errors.Is(err, store.ErrBookingState) {
		if cur, gerr := s.store.Bookings().Get(ctx, b.ID); gerr == nil && cur.Status == store.BookingConfirmed {
			s.writeJSON(w, http.StatusConflict, map[string]string{"error": "This booking is already confirmed", "code": "already_confirmed"})
			return
		}
		s.writeJSON(w, http.StatusGone, map[string]string{"error": "This hold has expired; book a time again", "code": "expired"})
		return
	}
	if s.bookingWriteFailed(w, err, b.ID) {
		return
	}
	s.writeJSON(w, http.StatusOK, s.bookingView(h, b, "confirmed"))
}

// bookingObject validates cal for the type's calendar as an ordinary write would.
func (s *Server) bookingObject(ctx context.Context, h *bookingHost, name string, cal *ical.Calendar) (*store.CalendarObject, *apiError) {
	c, err := s.store.Calendars().GetCalendarByID(ctx, h.t.CalendarID)
	if err != nil || c.OwnerKind != "user" || c.OwnerID != h.owner.ID || c.Subscribed || !c.Holds(ical.CompEvent) {
		return nil, &apiError{status: http.StatusConflict, msg: "This booking page is not set up to take bookings", code: "not_set_up"}
	}
	var raw strings.Builder
	if err := ical.NewEncoder(&raw).Encode(cal); err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to build the booking"}
	}
	o, err := davbackend.Prepare(c.ID, name, cal, []byte(raw.String()))
	if err != nil {
		return nil, &apiError{status: http.StatusInternalServerError, msg: "Failed to build the booking"}
	}
	o.WriterID = h.owner.ID
	return o, nil
}

// bookingWriteFailed answers a failed booking transaction; false when err is nil.
func (s *Server) bookingWriteFailed(w http.ResponseWriter, err error, id string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errSlotTaken):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "That time is no longer free; choose another", "code": "slot_unavailable"})
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "This calendar is full and cannot take the booking", "code": "quota"})
	case errors.Is(err, store.ErrPreconditionFailed), errors.Is(err, store.ErrBookingState):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "The booking changed; reload the page", "code": "conflict"})
	case errors.As(err, new(davbackend.InviteLimitError)), errors.As(err, new(store.InviteByteLimitError)):
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "This calendar cannot send invitations right now; try later", "code": "unavailable"})
	default:
		log.Printf("booking %s write failed: %v", id, err)
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The booking cannot be saved right now; try again later", "code": "unavailable"})
	}
	return true
}

// managed is the confirmed booking behind a manage link with its event, or false when the
// member already removed the event (the booking is then forgotten and shown cancelled).
func (s *Server) managed(w http.ResponseWriter, r *http.Request) (*linkBody, *store.Booking, *bookingHost, *store.CalendarObject, bool) {
	req, b, h, ok := s.linked(w, r, manageHash)
	if !ok {
		return nil, nil, nil, nil, false
	}
	o, err := s.store.Calendars().GetObject(r.Context(), b.CalendarID, b.ObjectName)
	if errors.Is(err, store.ErrNotFound) {
		_ = s.store.Bookings().Forget(context.WithoutCancel(r.Context()), b.ID)
		s.writeJSON(w, http.StatusOK, s.bookingView(h, b, "cancelled"))
		return nil, nil, nil, nil, false
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the booking")
		return nil, nil, nil, nil, false
	}
	return req, b, h, o, true
}

func (s *Server) handleBookingManage(w http.ResponseWriter, r *http.Request) {
	_, b, h, _, ok := s.managed(w, r)
	if !ok {
		return
	}
	v := s.bookingView(h, b, "confirmed")
	slots, err := s.slots(r.Context(), h, time.Now(), b)
	if err != nil {
		slots = nil
	}
	v["slots"] = slotViews(slots)
	s.writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleBookingCancel(w http.ResponseWriter, r *http.Request) {
	_, b, h, _, ok := s.managed(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	plan := s.sched.Plan(ctx, h.owner, nil)
	err := store.RetryStorageRefresh(ctx, func() error {
		return s.store.Bookings().Cancel(ctx, b.ID, s.objectLimits(), plan)
	})
	if s.bookingWriteFailed(w, err, b.ID) {
		return
	}
	s.writeJSON(w, http.StatusOK, s.bookingView(h, b, "cancelled"))
}

func (s *Server) handleBookingReschedule(w http.ResponseWriter, r *http.Request) {
	req, b, h, o, ok := s.managed(w, r)
	if !ok {
		return
	}
	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a time", "code": "invalid"})
		return
	}
	cal, err := ical.NewDecoder(strings.NewReader(string(o.Data))).Decode()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the booking")
		return
	}
	now := time.Now()
	slot := booking.Interval{Start: start, End: start.Add(h.rules.Duration)}
	if err := booking.Move(cal, slot, h.rules.Zone, now); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to move the booking")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	no, apiErr := s.bookingObject(ctx, h, o.Name, cal)
	if apiErr != nil {
		s.writeAPIError(w, apiErr)
		return
	}
	from, to := h.checkSpan(slot)
	plan := s.sched.Plan(ctx, h.owner, cal)
	err = store.RetryStorageRefresh(ctx, func() error {
		attempt := *no
		return s.store.Bookings().Reschedule(ctx, b.ID, slot.Start.Unix(), slot.End.Unix(), now.Unix(), from, to, h.offeredCheck(now, start), &attempt, o.ETag, s.objectLimits(), plan)
	})
	if s.bookingWriteFailed(w, err, b.ID) {
		return
	}
	b.Start, b.End = slot.Start.Unix(), slot.End.Unix()
	s.writeJSON(w, http.StatusOK, s.bookingView(h, b, "confirmed"))
}

// PruneBookings deletes expired holds, with what the visitor typed, and bookings ended 30 days
// ago. cmd/server runs it every minute.
func (s *Server) PruneBookings(ctx context.Context) {
	now := time.Now()
	if err := s.store.Bookings().Prune(ctx, now.Unix(), now.Add(-30*24*time.Hour).Unix()); err != nil {
		log.Printf("booking prune failed: %v", err)
	}
}
