package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// erasedSubject is one entry of KyIdentity's GET /api/suite/erased.
type erasedSubject struct {
	Sub      string `json:"sub"`
	OrgID    string `json:"org_id"`
	Revision int64  `json:"revision"`
}

// maxErasedPages bounds one read: 100 pages of 500, as suitedir.Orgs bounds its own.
const maxErasedPages = 100

// errErasedMalformed refuses a whole answer with any entry KyCalendar would not apply. It quotes
// no entry, so no subject or org ID is logged.
var errErasedMalformed = errors.New("suite directory: erased: malformed answer")

// suiteErased reads every erased subject from revision 0, page by page, with the app's client
// credentials, as suitedir does for /api/suite/orgs: HTTPS only (loopback HTTP for tests), no
// redirects, a bounded body, and a malformed entry fails the whole read. suitedir has no Erased
// method yet; this moves there when it does (KyPost carries the same reader).
func suiteErased(ctx context.Context, dir *suitedir.Client) ([]erasedSubject, error) {
	base, err := url.Parse(strings.TrimRight(dir.Issuer, "/"))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		base.Scheme != "https" && (base.Scheme != "http" || !loopbackHost(base.Hostname())) {
		return nil, suitedir.ErrInsecureIssuer
	}
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirects are refused")
	}}
	if dir.HTTP != nil {
		client.Transport = dir.HTTP.Transport
	}
	var out []erasedSubject
	since := int64(0)
	for range maxErasedPages {
		u := base.JoinPath("api", "suite", "erased")
		u.RawQuery = url.Values{"since": {strconv.FormatInt(since, 10)}, "limit": {"500"}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(dir.ClientID, dir.ClientSecret)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			return nil, fmt.Errorf("suite directory: erased: %w", err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
		_ = resp.Body.Close()
		switch {
		case err != nil:
			return nil, fmt.Errorf("suite directory: erased: %w", err)
		case resp.StatusCode == http.StatusUnauthorized:
			return nil, suitedir.ErrUnauthorized
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("suite directory: erased: status %d", resp.StatusCode)
		case len(raw) > 1<<20:
			return nil, errErasedMalformed
		}
		var body struct {
			Subjects []erasedSubject `json:"subjects"`
			Next     string          `json:"next"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Subjects == nil {
			return nil, errErasedMalformed
		}
		for _, e := range body.Subjects {
			if e.Sub == "" || len(e.Sub) > 255 || e.OrgID == "" || len(e.OrgID) > 64 || e.Revision <= since {
				return nil, errErasedMalformed
			}
			since = e.Revision
		}
		out = append(out, body.Subjects...)
		if body.Next == "" || len(body.Subjects) == 0 {
			return out, nil
		}
		if next, err := strconv.ParseInt(body.Next, 10, 64); err != nil || next != since {
			return nil, errErasedMalformed
		}
	}
	return nil, errors.New("suite directory: erased: more pages than one read allows")
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// eraseAfterRestore deletes every account KyIdentity lists as erased, as user.erased does for
// one (an account of another org than the entry's is left alone), then clears the pending mark.
// Any failure leaves the mark, so the restored orgs stay unserved and the next poll tries again.
func (s *Server) eraseAfterRestore(ctx context.Context, dir *suitedir.Client) error {
	subjects, err := suiteErased(ctx, dir)
	if err != nil {
		return err
	}
	bound, err := sso.Bound(ctx, s.store.Settings())
	if err != nil {
		return err
	}
	for _, e := range subjects {
		u, err := s.directoryAccount(ctx, bound, e.Sub)
		if err != nil {
			return err
		}
		if u == nil || store.OrgOf(u) != store.OrgID(e.OrgID) {
			continue
		}
		if err := s.eraseAccount(ctx, u.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("erased subject at revision %d: %w", e.Revision, err)
		}
	}
	return s.store.Settings().DeleteSetting(ctx, store.ErasedPendingSetting)
}
