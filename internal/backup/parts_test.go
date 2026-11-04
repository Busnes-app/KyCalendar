package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
)

// A database larger than one capsule member travels in ordered parts (DECISIONS: large
// databases in backups); the drill joins them and checks the database a restore would get.
func TestLargeDatabaseTravelsInPartsAndDrills(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	defer backup.SetPartBytesForTest(4096)()
	cfg, _ := payloadConfig(t)
	payload, err := backup.Collect(context.Background(), cfg, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, f := range payload.Files {
		if f.Path == backup.DatabaseMember {
			t.Fatal("a split database also travels whole")
		}
		if strings.HasPrefix(f.Path, backup.DatabaseMember+".part-") {
			parts = append(parts, f.Path)
		}
	}
	if len(parts) < 2 || parts[0] != backup.DatabaseMember+".part-0001" {
		t.Fatalf("parts: %v", parts)
	}
	root := backup.DrillRoot(cfg)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := recoveryclient.Drill(context.Background(), root, payload, backup.Checks)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("drill of a split database failed: %+v", result)
	}

	// A capsule missing one part fails its drill.
	payload.Files = payload.Files[1:]
	payload.VerificationRecipe["required_files"] = func() []string {
		var r []string
		for _, f := range payload.Files {
			r = append(r, f.Path)
		}
		return append(r, backup.DatabaseMember)
	}()
	result, err = recoveryclient.Drill(context.Background(), root, payload, backup.Checks)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("a drill passed with a database part missing")
	}
}

// A restore joins the parts into the database, in order, and removes them; a gap is refused.
func TestJoinRestoredDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i, s := range []string{"one-", "two-", "three"} {
		if err := os.WriteFile(filepath.Join(dir, "kycalendar.db.part-000"+string(rune('1'+i))), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinRestoredDatabase(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "kycalendar.db")); err != nil || !bytes.Equal(got, []byte("one-two-three")) {
		t.Fatalf("joined database %q %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.part-*")); len(left) != 0 {
		t.Fatalf("parts left behind: %v", left)
	}
	if err := backup.JoinRestoredDatabase(dir); err != nil {
		t.Fatalf("joining again with nothing to join: %v", err)
	}

	// Interrupted: the database was joined and only the last part removed. Run again, the
	// restore keeps the joined database and removes the rest; an unrelated database is refused.
	resume := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(resume, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"kycalendar.db": "one-two-three", "kycalendar.db.part-0001": "one-", "kycalendar.db.part-0002": "two-"} {
		if err := os.WriteFile(filepath.Join(resume, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinRestoredDatabase(resume); err != nil {
		t.Fatalf("resuming an interrupted join: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(resume, "*.part-*")); len(left) != 0 {
		t.Fatalf("parts left behind after resuming: %v", left)
	}
	if err := os.WriteFile(filepath.Join(resume, "kycalendar.db.part-0001"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := backup.JoinRestoredDatabase(resume); err == nil {
		t.Fatal("a database that is not the join of its parts was kept")
	}

	gap := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(gap, 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0001", "0003"} {
		if err := os.WriteFile(filepath.Join(gap, "kycalendar.db.part-"+n), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.JoinRestoredDatabase(gap); err == nil {
		t.Fatal("a missing part was joined over")
	}
}
