package backup

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/emersion/go-ical"
	_ "modernc.org/sqlite"
)

// Checks validates the recipe from the capsule that was actually opened. A malformed
// recipe is a failed drill, never permission to omit a required product check.
func Checks(dir string, opened capsule.Manifest) []recoveryclient.Check {
	recipe, ok := opened.VerificationRecipe.(map[string]any)
	if !ok {
		return recipeFailure("Expected a recipe object")
	}
	required, err := recipeStrings(recipe["required_files"])
	if err != nil {
		return recipeFailure("required_files: " + err.Error())
	}
	sqlitePaths, err := recipeStrings(recipe["sqlite_paths"])
	if err != nil {
		return recipeFailure("sqlite_paths: " + err.Error())
	}
	env, err := recipeStrings(recipe["expected_env"])
	if err != nil {
		return recipeFailure("expected_env: " + err.Error())
	}
	if enabled, ok := recipe["check_sqlite_integrity"].(bool); !ok || !enabled {
		return recipeFailure("check_sqlite_integrity must be true")
	}
	for _, name := range []string{DatabaseMember, "config/settings.json", encryptionKeyPath} {
		if !slices.Contains(required, name) {
			return recipeFailure("required_files omits " + name)
		}
	}
	if !slices.Contains(sqlitePaths, DatabaseMember) {
		return recipeFailure("sqlite_paths omits the database")
	}
	for _, name := range []string{"KY_PORT", "KY_DB_DRIVER"} {
		if !slices.Contains(env, name) {
			return recipeFailure("expected_env omits " + name)
		}
	}
	wantCalendars, err := recipeCount(recipe, "calendar_count")
	if err != nil {
		return recipeFailure(err.Error())
	}
	wantObjects, err := recipeCount(recipe, "object_count")
	if err != nil {
		return recipeFailure(err.Error())
	}
	members := make(map[string]bool, len(opened.Files))
	for _, file := range opened.Files {
		members[file.Path] = true
		if !slices.Contains(required, file.Path) {
			return recipeFailure("required_files omits a capsule member")
		}
	}
	for _, paths := range [][]string{required, sqlitePaths} {
		for _, name := range paths {
			if _, safe := drillPath(dir, name); !safe || !members[name] {
				return recipeFailure("File check must name a clean relative capsule member")
			}
		}
	}
	var checks []recoveryclient.Check
	allFound := true
	for _, name := range required {
		full, _ := drillPath(dir, name)
		fi, err := os.Lstat(full)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			allFound = false
			checks = append(checks, recoveryclient.Check{Name: "Required File: " + name, Message: "File missing, empty or not regular"})
		}
	}
	if allFound {
		checks = append(checks, recoveryclient.Check{Name: "Required Files", Passed: true, Message: fmt.Sprintf("All %d required files verified", len(required))})
	}
	for _, name := range sqlitePaths {
		full, _ := drillPath(dir, name)
		checks = append(checks, sqliteIntegrityCheck(name, full))
	}
	full, _ := drillPath(dir, DatabaseMember)
	checks = append(checks, calendarChecks(full, wantCalendars, wantObjects)...)
	for _, name := range env {
		_, found := os.LookupEnv(name)
		message := "Missing"
		if found {
			message = "Configured"
		}
		checks = append(checks, recoveryclient.Check{Name: "Environment: " + name, Passed: found, Message: message})
	}
	return checks
}

func recipeFailure(message string) []recoveryclient.Check {
	return []recoveryclient.Check{{Name: "Verification Recipe", Message: message}}
}

// Open JSON-decodes lists as []any. []string also supports in-memory fixtures.
func recipeStrings(value any) ([]string, error) {
	var result []string
	switch list := value.(type) {
	case []string:
		result = list
	case []any:
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected strings")
			}
			result = append(result, s)
		}
	default:
		return nil, fmt.Errorf("expected a nonempty string list")
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("expected a nonempty string list")
	}
	for _, item := range result {
		if strings.TrimSpace(item) == "" || strings.ContainsRune(item, 0) {
			return nil, fmt.Errorf("invalid empty or NUL-containing member")
		}
	}
	return result, nil
}

func sqliteIntegrityCheck(name, path string) recoveryclient.Check {
	fail := func(message string) recoveryclient.Check {
		return recoveryclient.Check{Name: "SQLite Integrity: " + name, Message: message}
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return fail("Database missing, empty or not regular")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fail("Invalid database path")
	}
	db, err := openReadOnly(absolute)
	if err != nil {
		return fail("Failed to open database")
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check;").Scan(&result); err != nil || result != "ok" {
		return fail("PRAGMA integrity_check failed")
	}
	return recoveryclient.Check{Name: "SQLite Integrity: " + name, Passed: true, Message: "PRAGMA integrity_check passed ok"}
}

// openReadOnly opens an absolute path as a read-only SQLite file. URL encoding prevents a
// filename's '?' or '#' from changing SQLite's options.
func openReadOnly(absolute string) (*sql.DB, error) {
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: "mode=ro"}).String()
	return sql.Open("sqlite", dsn)
}

// recipeCount reads a count that JSON decodes as float64 and fixtures may hold as an int.
func recipeCount(recipe map[string]any, key string) (int64, error) {
	bad := fmt.Errorf("%s must be a non-negative integer", key)
	switch v := recipe[key].(type) {
	case int:
		if v >= 0 {
			return int64(v), nil
		}
	case int64:
		if v >= 0 {
			return v, nil
		}
	case float64:
		if v >= 0 && v == math.Trunc(v) && v < 1<<53 {
			return int64(v), nil
		}
	}
	return 0, bad
}

// maxParsedObjects bounds the parse sample so a large calendar cannot stall the drill.
const maxParsedObjects = 50

// calendarChecks compares the restored database's row counts with the recipe and parses a
// sample of stored objects. Messages carry IDs and counts, never object data.
func calendarChecks(path string, wantCalendars, wantObjects int64) []recoveryclient.Check {
	counts := func(ok bool, msg string) recoveryclient.Check {
		return recoveryclient.Check{Name: "Calendar Counts", Passed: ok, Message: msg}
	}
	objects := func(ok bool, msg string) recoveryclient.Check {
		return recoveryclient.Check{Name: "Calendar Objects", Passed: ok, Message: msg}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return []recoveryclient.Check{counts(false, "Invalid database path")}
	}
	db, err := openReadOnly(absolute)
	if err != nil {
		return []recoveryclient.Check{counts(false, "Failed to open database")}
	}
	defer db.Close()
	got, err := countCalendars(context.Background(), db)
	if err != nil {
		return []recoveryclient.Check{counts(false, "Cannot count calendars and objects")}
	}
	var out []recoveryclient.Check
	if got.Calendars != wantCalendars || got.Objects != wantObjects {
		out = append(out, counts(false, fmt.Sprintf("Restored %d calendars and %d objects, recipe says %d and %d", got.Calendars, got.Objects, wantCalendars, wantObjects)))
	} else {
		out = append(out, counts(true, fmt.Sprintf("%d calendars and %d objects match the recipe", got.Calendars, got.Objects)))
	}
	if got.Objects == 0 {
		return append(out, objects(true, "No objects to parse"))
	}
	rows, err := db.Query(`SELECT calendar_id, data FROM calendar_objects ORDER BY calendar_id, name LIMIT ?`, maxParsedObjects)
	if err != nil {
		return append(out, objects(false, "Cannot read objects"))
	}
	defer rows.Close()
	parsed := 0
	for rows.Next() {
		var calID string
		var data []byte
		if err := rows.Scan(&calID, &data); err != nil {
			return append(out, objects(false, "Cannot read objects"))
		}
		cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
		if err == nil {
			_, err = calendar.Inspect(cal)
		}
		if err != nil {
			// The object name is client-chosen text; report its position in the sample.
			return append(out, objects(false, fmt.Sprintf("Cannot parse object %d of %d in %s", parsed+1, min(got.Objects, maxParsedObjects), calID)))
		}
		parsed++
	}
	if rows.Err() != nil {
		return append(out, objects(false, "Cannot read objects"))
	}
	return append(out, objects(true, fmt.Sprintf("Parsed %d of %d objects", parsed, got.Objects)))
}

func drillPath(root, name string) (string, bool) {
	clean := filepath.Clean(name)
	if clean != name || clean == "." || !filepath.IsLocal(name) || strings.ContainsAny(name, "\\\x00") {
		return "", false
	}
	return filepath.Join(root, name), true
}

// DrillRoot keeps opened instance data beneath the deployment's data directory.
func DrillRoot(cfg *config.Config) string { return filepath.Join(cfg.Database.DataDir, "drill") }
