package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
)

// The production deadline is 14 s and below the listener's write timeout, so its 503 still
// reaches the client (spec AC3).
func TestRequestDeadlineFitsTheWriteTimeout(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if requestDeadline != 14*time.Second || requestDeadline >= cfg.Server.WriteTimeout {
		t.Fatalf("request deadline %v, write timeout %v; want 14 s and below it", requestDeadline, cfg.Server.WriteTimeout)
	}
}

// A handler that ignores its context still gets its answer replaced by the 503 at the
// deadline, with the security headers every answer carries.
func TestStalledHandlerAnswersAtTheDeadline(t *testing.T) {
	s, _ := davInternalServer(t)
	old := requestDeadline
	requestDeadline = 300 * time.Millisecond
	defer func() { requestDeadline = old }()
	release := make(chan struct{})
	defer close(release)
	s.mux.HandleFunc("GET /test/stall", func(w http.ResponseWriter, _ *http.Request) {
		<-release // ignores its context
		w.WriteHeader(http.StatusOK)
	})
	h := s.withDeadline(http.HandlerFunc(s.serve), requestDeadline)
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/test/stall", nil))
	if d := time.Since(start); w.Code != http.StatusServiceUnavailable || d > 1300*time.Millisecond {
		t.Fatalf("stalled handler: %d after %v", w.Code, d)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if w.Header().Get(h) == "" {
			t.Errorf("the deadline's 503 lacks %s", h)
		}
	}
}
