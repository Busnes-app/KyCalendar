package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestPersonalCalendarLifecycle(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "pia", "user")
	w := call(t, srv, "POST", "/api/calendars", `{"name":"Work","color":"#00aa11"}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var c struct{ ID, Name, Kind, Role string }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if c.Kind != "personal" || c.Role != "owner" {
		t.Fatalf("created %+v", c)
	}
	if w := call(t, srv, "PATCH", "/api/calendars/"+c.ID, `{"name":"Office","color":""}`, cookie); w.Code != http.StatusOK {
		t.Fatalf("patch %d %s", w.Code, w.Body.String())
	}
	got, _ := st.Calendars().GetCalendarByID(context.Background(), c.ID)
	if got.Name != "Office" || got.Color != "" {
		t.Fatalf("patched %+v", got)
	}
	for _, body := range []string{`{"name":"  "}`, `{"color":"red"}`, `nope`} {
		if w := call(t, srv, "PATCH", "/api/calendars/"+c.ID, body, cookie); w.Code != http.StatusBadRequest {
			t.Errorf("patch %s: %d, want 400", body, w.Code)
		}
	}
	if w := call(t, srv, "DELETE", "/api/calendars/"+c.ID, "", cookie); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("calendar survived: %v", err)
	}
}

func TestGroupCalendarPatchAndDeleteRules(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	group := groupCalendar(t, st, "Team")
	mgr := loginAs(t, srv, st, "mia", "user")
	ed := loginAs(t, srv, st, "eli", "user")
	grantRole(t, st, group, "manager", "usr_mia")
	grantRole(t, st, group, "editor", "usr_eli")
	if w := call(t, srv, "PATCH", "/api/calendars/"+group.ID, `{"name":"Team A"}`, mgr); w.Code != http.StatusOK {
		t.Fatalf("manager rename %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/calendars/"+group.ID, `{"name":"x"}`, ed); w.Code != http.StatusForbidden {
		t.Fatalf("editor rename %d, want 403", w.Code)
	}
	if w := call(t, srv, "DELETE", "/api/calendars/"+group.ID, "", mgr); w.Code != http.StatusForbidden {
		t.Fatalf("manager delete of a group calendar %d, want 403 (admins delete them)", w.Code)
	}
	other := loginAs(t, srv, st, "oz", "user")
	if w := call(t, srv, "DELETE", "/api/calendars/"+group.ID, "", other); w.Code != http.StatusNotFound {
		t.Fatalf("non-member delete %d, want 404", w.Code)
	}
}
