package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

var (
	driveReport = map[string]any{"name": "Q3 report.pdf", "url": "https://drive.example/l/#tok1", "type": "application/pdf"}
	driveSheet  = map[string]any{"name": "Budget", "url": "https://drive.example/l/#tok2"}
)

// File links (contract I) are stored as ATTACH and "File:" lines, listed by the view with the
// lines taken out of the description, and kept by web edits that do not name them.
func TestEventFilesAreStoredListedAndKept(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"description": "Agenda", "files": []any{driveReport, driveSheet}})
	data := string(r.object(t, uid).Data)
	for _, want := range []string{
		"ATTACH;FILENAME=Q3 report.pdf;FMTTYPE=application/pdf:https://drive.example/l/#tok1",
		"ATTACH;FILENAME=Budget:https://drive.example/l/#tok2",
		`File: Q3 report.pdf https://drive.example/l/#tok1\nFile: Budget https://drive.example/l/#tok2`,
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("no %q in\n%s", want, data)
		}
	}
	v := r.view(t, uid)
	if v.Description != "Agenda" || len(v.Files) != 2 || v.Files[0] != (fileView{Name: "Q3 report.pdf", URL: "https://drive.example/l/#tok1", Type: "application/pdf"}) {
		t.Fatalf("view %q %+v", v.Description, v.Files)
	}
	// An edit that does not name files keeps them and their lines.
	if w := r.update(t, uid, map[string]any{"description": "New agenda"}); w.Code != http.StatusOK {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	if v := r.view(t, uid); v.Description != "New agenda" || len(v.Files) != 2 {
		t.Fatalf("after edit %q %+v", v.Description, v.Files)
	}
	if !strings.Contains(string(r.object(t, uid).Data), `New agenda\n\nFile: Q3 report.pdf`) {
		t.Fatalf("lines lost:\n%s", r.object(t, uid).Data)
	}
	if w := r.update(t, uid, map[string]any{"files": []any{driveSheet}}); w.Code != http.StatusOK {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	if v := r.view(t, uid); len(v.Files) != 1 || v.Files[0].Name != "Budget" || v.Description != "New agenda" {
		t.Fatalf("after removing one %+v", v)
	}
	if w := r.update(t, uid, map[string]any{"files": []any{}}); w.Code != http.StatusOK {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	if data := string(r.object(t, uid).Data); strings.Contains(data, "ATTACH") || strings.Contains(data, "File:") {
		t.Fatalf("not removed:\n%s", data)
	}
}

// A type that could break out of the FMTTYPE parameter is dropped, never refused and never written.
func TestEventFileTypesAreDroppedUnlessPlain(t *testing.T) {
	r := newMeetRig(t)
	hostile := []string{`text/plain"x`, "text/html;charset=utf-8", "text/x:evil", "text/plain\r\nATTACH:https://evil.example",
		"Text/Plain", "text/plain x", "application/" + strings.Repeat("x", 116), "/", "text/"}
	files := []any{driveReport}
	for i, typ := range hostile {
		files = append(files, map[string]any{"name": fmt.Sprintf("f%d", i), "url": fmt.Sprintf("https://drive.example/l/#h%d", i), "type": typ})
	}
	uid := r.create(t, map[string]any{"files": files})
	data := string(r.object(t, uid).Data)
	if n := strings.Count(data, "FMTTYPE"); n != 1 || !strings.Contains(data, "FMTTYPE=application/pdf:") || strings.Contains(data, "evil") || strings.Contains(data, "charset") {
		t.Fatalf("%d FMTTYPE in\n%s", n, data)
	}
	v := r.view(t, uid)
	if len(v.Files) != len(files) || v.Files[0].Type != "application/pdf" {
		t.Fatalf("view %+v", v.Files)
	}
	for _, f := range v.Files[1:] {
		if f.Type != "" {
			t.Errorf("kept type %q", f.Type)
		}
	}
}

func TestEventFilesAreCheckedAtTheBoundary(t *testing.T) {
	r := newMeetRig(t)
	many := make([]any, 21)
	for i := range many {
		many[i] = driveSheet
	}
	for name, files := range map[string]any{
		"http link":      []any{map[string]any{"name": "a", "url": "http://drive.example/l/#x"}},
		"script link":    []any{map[string]any{"name": "a", "url": "javascript:alert(1)"}},
		"credentials":    []any{map[string]any{"name": "a", "url": "https://u:p@drive.example/"}},
		"no name":        []any{map[string]any{"name": " ", "url": "https://drive.example/l/#x"}},
		"quote in name":  []any{map[string]any{"name": `a"b`, "url": "https://drive.example/l/#x"}},
		"newline":        []any{map[string]any{"name": "a\nFile: evil https://evil.example", "url": "https://drive.example/l/#x"}},
		"long name":      []any{map[string]any{"name": strings.Repeat("a", 256), "url": "https://drive.example/l/#x"}},
		"too many files": many,
	} {
		ev := map[string]any{"title": "T", "start": "2026-11-16T09:00:00Z", "end": "2026-11-16T10:00:00Z", "zone": "UTC", "files": files}
		if w := r.do(t, r.org, http.MethodPost, "/", ev, r.s.handleCreateEvent, map[string]string{"id": r.cal.ID}, ""); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "bad_files") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	uid := r.create(t, map[string]any{"repeat": map[string]any{"freq": "daily"}})
	v := r.view(t, uid)
	ev := map[string]any{"title": "T", "start": v.Start, "end": v.End, "zone": "UTC", "scope": "this", "recurrence_id": v.RecurrenceID, "repeat": v.Repeat, "files": []any{driveSheet}}
	if w := r.do(t, r.org, http.MethodPut, "/", ev, r.s.handleUpdateEvent, map[string]string{"cal": r.cal.ID, "uid": uid}, r.object(t, uid).ETag); w.Code != http.StatusBadRequest {
		t.Fatalf("one occurrence: %d %s", w.Code, w.Body)
	}
}
