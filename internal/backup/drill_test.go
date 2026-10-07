package backup_test

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
	_ "modernc.org/sqlite"
)

func TestChecksFailsOnAScratchDirMissingTheDatabase(t *testing.T) {
	cfg, _ := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	scratch := t.TempDir()
	// Recreate every required file except the database, so the missing member is the only
	// difference from a real drill's scratch directory.
	for _, f := range payload.Files {
		if f.Path == backup.DatabaseMember {
			continue
		}
		full := filepath.Join(scratch, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	checks := backup.Checks(scratch, manifestFor(payload))
	var sawMissing bool
	for _, c := range checks {
		if c.Name == "Required File: "+backup.DatabaseMember {
			sawMissing = true
			if c.Passed {
				t.Error("missing database reported as passed")
			}
		}
		if c.Passed && c.Name == "Required Files" {
			t.Error("Required Files reported all-present with the database missing")
		}
	}
	for _, check := range checks {
		if check.Name == "SQLite Integrity: "+backup.DatabaseMember && check.Passed {
			t.Error("missing SQLite database passed integrity checking")
		}
	}
	if _, err := os.Stat(filepath.Join(scratch, backup.DatabaseMember)); !os.IsNotExist(err) {
		t.Fatalf("integrity check created the missing database: %v", err)
	}
	if !sawMissing {
		t.Fatal("no check reported the missing database member")
	}
}

func TestChecksPassesOnACompleteScratchDir(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, _ := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	for _, f := range payload.Files {
		full := filepath.Join(scratch, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	checks := backup.Checks(scratch, manifestFor(payload))
	if len(checks) == 0 {
		t.Fatal("no checks ran")
	}
	for _, c := range checks {
		if !c.Passed {
			t.Errorf("check %q failed: %s", c.Name, c.Message)
		}
	}
}

func TestDrillRootIsUnderTheDataDir(t *testing.T) {
	cfg, _ := payloadConfig(t)
	root := backup.DrillRoot(cfg)
	rel, err := filepath.Rel(cfg.Database.DataDir, root)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("drill root %q is not under data dir %q", root, cfg.Database.DataDir)
	}
}

func manifestFor(payload recoveryclient.Payload) capsule.Manifest {
	m := capsule.Manifest{UnverifiedManifest: capsule.UnverifiedManifest{VerificationRecipe: payload.VerificationRecipe}}
	for _, f := range payload.Files {
		m.Files = append(m.Files, capsule.FileEntry{Path: f.Path})
	}
	return m
}

func TestDrillChecksDecodedManifest(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, _ := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	root := backup.DrillRoot(cfg)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	var scratch string
	result, err := recoveryclient.Drill(context.Background(), root, payload, func(dir string, opened capsule.Manifest) []recoveryclient.Check {
		scratch = dir
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("scratch permissions %o", info.Mode().Perm())
		}
		recipe, ok := opened.VerificationRecipe.(map[string]any)
		if !ok {
			t.Fatalf("recipe type %T", opened.VerificationRecipe)
		}
		if _, ok := recipe["required_files"].([]any); !ok {
			t.Fatalf("list was not JSON decoded: %T", recipe["required_files"])
		}
		// Mutating the original recipe cannot change what the opened capsule checks.
		payload.VerificationRecipe["required_files"] = nil
		return backup.Checks(dir, opened)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("drill failed: %+v", result)
	}
	for _, name := range []string{"Required Files", "SQLite Integrity: " + backup.DatabaseMember, "Environment: KY_PORT", "Environment: KY_DB_DRIVER"} {
		found := false
		for _, c := range result.Checks {
			if c.Name == name && c.Passed {
				found = true
			}
		}
		if !found {
			t.Errorf("missing passing check %q: %+v", name, result.Checks)
		}
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch survived: %v", err)
	}
}

func TestDrillRejectsMalformedRecipes(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, _ := payloadConfig(t)
	original, err := backup.Collect(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any){
		"missing required":     func(r map[string]any) { delete(r, "required_files") },
		"null required":        func(r map[string]any) { r["required_files"] = nil },
		"empty required":       func(r map[string]any) { r["required_files"] = []string{} },
		"wrong required type":  func(r map[string]any) { r["required_files"] = backup.DatabaseMember },
		"mixed required":       func(r map[string]any) { r["required_files"] = []any{backup.DatabaseMember, 42} },
		"omitted key":          func(r map[string]any) { r["required_files"] = []string{backup.DatabaseMember, "config/settings.json"} },
		"missing sqlite flag":  func(r map[string]any) { delete(r, "check_sqlite_integrity") },
		"false sqlite flag":    func(r map[string]any) { r["check_sqlite_integrity"] = false },
		"wrong sqlite flag":    func(r map[string]any) { r["check_sqlite_integrity"] = "true" },
		"missing sqlite paths": func(r map[string]any) { delete(r, "sqlite_paths") },
		"empty sqlite paths":   func(r map[string]any) { r["sqlite_paths"] = []string{} },
		"null sqlite paths":    func(r map[string]any) { r["sqlite_paths"] = nil },
		"mixed sqlite paths":   func(r map[string]any) { r["sqlite_paths"] = []any{backup.DatabaseMember, false} },
		"sqlite omits db":      func(r map[string]any) { r["sqlite_paths"] = []string{"config/settings.json"} },
		"missing env":          func(r map[string]any) { delete(r, "expected_env") },
		"empty env":            func(r map[string]any) { r["expected_env"] = []string{} },
		"null env":             func(r map[string]any) { r["expected_env"] = nil },
		"wrong env":            func(r map[string]any) { r["expected_env"] = true },
		"mixed env":            func(r map[string]any) { r["expected_env"] = []any{"KY_PORT", 1} },
		"omitted env":          func(r map[string]any) { r["expected_env"] = []string{"KY_PORT"} },
	}
	for _, path := range []string{"", ".", "../outside", "/etc/passwd", "data/../" + backup.DatabaseMember, "data//kycalendar.db", "data\\kycalendar.db", "data/not-in-manifest", "data/\x00db"} {
		cases["unsafe path "+path] = func(r map[string]any) {
			r["required_files"] = append(append([]string{}, r["required_files"].([]string)...), path)
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			payload := original
			payload.VerificationRecipe = maps.Clone(original.VerificationRecipe)
			mutate(payload.VerificationRecipe)
			result, err := backup.RunDrill(context.Background(), cfg, payload)
			if err != nil {
				t.Fatal(err)
			}
			if result.Passed {
				t.Fatalf("malformed recipe passed: %+v", result)
			}
			var opened, failed bool
			for _, c := range result.Checks {
				if c.Name == "Directory Unpack" && c.Passed {
					opened = true
				}
				if c.Name == "Verification Recipe" && !c.Passed {
					failed = true
				}
			}
			if !opened || !failed {
				t.Fatalf("did not fail the opened recipe: %+v", result)
			}
			entries, err := os.ReadDir(backup.DrillRoot(cfg))
			if err != nil || len(entries) != 0 {
				t.Fatalf("scratch not cleaned: %v %v", entries, err)
			}
		})
	}
	for _, recipe := range []any{nil, "invalid", []any{}} {
		m := manifestFor(original)
		m.VerificationRecipe = recipe
		checks := backup.Checks(t.TempDir(), m)
		if len(checks) != 1 || checks[0].Passed {
			t.Fatalf("nonobject recipe passed: %+v", checks)
		}
	}
}

func TestDrillRejectsDamagedPayload(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	for _, kind := range []string{"missing database", "empty database", "corrupt database", "missing environment"} {
		t.Run(kind, func(t *testing.T) {
			cfg, _ := payloadConfig(t)
			payload, err := backup.Collect(context.Background(), cfg, "test")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "missing environment" {
				payload.VerificationRecipe["expected_env"] = []string{"KY_PORT", "KY_DB_DRIVER", "KY_DRILL_TEST_MISSING"}
				t.Setenv("KY_DRILL_TEST_MISSING", "temporary")
				os.Unsetenv("KY_DRILL_TEST_MISSING")
			}
			for i, f := range payload.Files {
				if f.Path == backup.DatabaseMember {
					switch kind {
					case "missing database":
						payload.Files = append(payload.Files[:i:i], payload.Files[i+1:]...)
					case "empty database":
						payload.Files[i].Data = nil
					case "corrupt database":
						payload.Files[i].Data = []byte("not a sqlite database")
					}
					break
				}
			}
			result, err := backup.RunDrill(context.Background(), cfg, payload)
			if err != nil {
				t.Fatal(err)
			}
			if result.Passed {
				t.Fatalf("damaged payload passed: %+v", result)
			}
		})
	}
}

func TestChecksSQLiteFilenameIsNotADSN(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, _ := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	const name = "data/extra?mode=rw#database.db"
	payload.Files = append(payload.Files, recoveryclient.File{Path: name, Data: payload.Files[0].Data, Mode: 0600})
	payload.VerificationRecipe["required_files"] = append(payload.VerificationRecipe["required_files"].([]string), name)
	payload.VerificationRecipe["sqlite_paths"] = []string{backup.DatabaseMember, name}
	result, err := backup.RunDrill(context.Background(), cfg, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("escaped filename failed: %+v", result)
	}
}

const validEvent = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260102T100000Z\r\nDTEND:20260102T110000Z\r\nSUMMARY:secret-summary\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// seedCalendars inserts the given calendar IDs and, per calendar, the named objects.
func seedCalendars(t *testing.T, dsn string, objects map[string]map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, objs := range objects {
		if _, err := db.Exec(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, created_at) VALUES (?, 'user', ?, ?, 'n', '2026-01-01T00:00:00Z')`, id, id, id); err != nil {
			t.Fatal(err)
		}
		for name, data := range objs {
			if _, err := db.Exec(`INSERT INTO calendar_objects (calendar_id, name, uid, etag, data, first_start, modified_at) VALUES (?, ?, ?, 'e', ?, 0, '2026-01-01T00:00:00Z')`, id, name, name, []byte(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func calendarCheckResults(t *testing.T, objects map[string]map[string]string, mutate func(map[string]any)) map[string]recoveryclient.Check {
	t.Helper()
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, _ := payloadConfig(t)
	seedCalendars(t, cfg.Database.DSN, objects)
	payload, err := backup.Collect(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	for _, f := range payload.Files {
		full := filepath.Join(scratch, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	payload.VerificationRecipe = maps.Clone(payload.VerificationRecipe)
	if mutate != nil {
		mutate(payload.VerificationRecipe)
	}
	out := map[string]recoveryclient.Check{}
	for _, c := range backup.Checks(scratch, manifestFor(payload)) {
		out[c.Name] = c
	}
	return out
}

func twoCalendars() map[string]map[string]string {
	return map[string]map[string]string{
		"cal-a": {"a1.ics": fmt.Sprintf(validEvent, "a1"), "a2.ics": fmt.Sprintf(validEvent, "a2")},
		"cal-b": {"b1.ics": fmt.Sprintf(validEvent, "b1")},
	}
}

func TestDrillCalendarCountsAndObjectsPass(t *testing.T) {
	got := calendarCheckResults(t, twoCalendars(), nil)
	for _, name := range []string{"Calendar Counts", "Calendar Objects"} {
		if !got[name].Passed {
			t.Errorf("%s: %+v", name, got[name])
		}
	}
	if got["Calendar Objects"].Message != "Parsed 3 of 3 objects" {
		t.Errorf("message %q", got["Calendar Objects"].Message)
	}
}

func TestDrillCalendarCountMismatchNamesBothNumbers(t *testing.T) {
	got := calendarCheckResults(t, twoCalendars(), func(r map[string]any) { r["object_count"] = int64(4) })
	c := got["Calendar Counts"]
	if c.Passed || !strings.Contains(c.Message, "3") || !strings.Contains(c.Message, "4") {
		t.Fatalf("%+v", c)
	}
}

func TestDrillCalendarRecipeCountsAreStrict(t *testing.T) {
	for _, key := range []string{"calendar_count", "object_count"} {
		for name, v := range map[string]any{"missing": nil, "negative": int64(-1), "fractional": 1.5, "string": "2"} {
			t.Run(key+" "+name, func(t *testing.T) {
				got := calendarCheckResults(t, nil, func(r map[string]any) {
					if v == nil {
						delete(r, key)
					} else {
						r[key] = v
					}
				})
				c := got["Verification Recipe"]
				if c.Passed || c.Message != key+" must be a non-negative integer" {
					t.Fatalf("%+v", c)
				}
			})
		}
	}
}

func TestDrillCalendarAcceptsJSONNumbers(t *testing.T) {
	got := calendarCheckResults(t, twoCalendars(), func(r map[string]any) {
		r["calendar_count"] = float64(2)
		r["object_count"] = float64(3)
	})
	if !got["Calendar Counts"].Passed {
		t.Fatalf("%+v", got["Calendar Counts"])
	}
}

func TestDrillBrokenObjectNamesIDsNotData(t *testing.T) {
	objs := twoCalendars()
	objs["cal-b"]["b1.ics"] = "BEGIN:VCALENDAR\r\nBROKEN secret-body"
	c := calendarCheckResults(t, objs, nil)["Calendar Objects"]
	if c.Passed || c.Message != "Cannot parse object 3 of 3 in cal-b" {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(c.Message, "secret") || strings.Contains(c.Message, "BROKEN") || strings.Contains(c.Message, "b1.ics") {
		t.Fatal("message carries object data or name")
	}
}

func TestDrillEmptyCalendarDatabasePasses(t *testing.T) {
	got := calendarCheckResults(t, nil, nil)
	if !got["Calendar Counts"].Passed || !got["Calendar Objects"].Passed || got["Calendar Objects"].Message != "No objects to parse" {
		t.Fatalf("%+v", got)
	}
}
