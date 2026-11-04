package backup

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
)

// partPrefix names the ordered members a database larger than one capsule member travels in:
// data/kycalendar.db.part-0001, -0002, ... Concatenated in order they are the database. A
// database that fits stays the single member DatabaseMember, as every older capsule has it.
const partPrefix = DatabaseMember + ".part-"

// partBytes is a variable so tests can split a small database.
var partBytes = int(recoveryclient.MaxCapsuleFileBytes)

// databaseFiles is the capsule members for db and, when it is split, their names in order.
func databaseFiles(db []byte) ([]recoveryclient.File, []string) {
	if len(db) <= partBytes {
		return []recoveryclient.File{{Path: DatabaseMember, Data: db, Mode: 0600}}, nil
	}
	var files []recoveryclient.File
	var names []string
	for i := 0; len(db) > 0; i++ {
		n := min(partBytes, len(db))
		name := fmt.Sprintf("%s%04d", partPrefix, i+1)
		files = append(files, recoveryclient.File{Path: name, Data: db[:n], Mode: 0600})
		names = append(names, name)
		db = db[n:]
	}
	return files, names
}

// warnNearLimit logs when a payload passes 80% of what one capsule can carry.
func warnNearLimit(files []recoveryclient.File) {
	total := 0
	for _, f := range files {
		total += len(f.Data)
	}
	if limit := int(recoveryclient.MaxCapsuleTotalBytes); total > limit/10*8 {
		log.Printf("[BACKUP] the backup is %d MiB, over 80%% of the %d MiB a capsule can carry; past it every backup fails. Remove old calendars or events.", total>>20, limit>>20)
	}
}

// joinDatabase writes dir's database parts, if any, into DatabaseMember in order and returns
// their paths. Parts must run from 0001 without a gap; a database already in place is an
// error, so a restore never mixes two. Nothing to join is not an error.
func joinDatabase(dir string) ([]string, error) {
	parts, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(partPrefix)) + "*")
	if err != nil || len(parts) == 0 {
		return nil, err
	}
	slices.Sort(parts)
	for i, p := range parts {
		if want := fmt.Sprintf("%s%04d", filepath.Base(partPrefix), i+1); filepath.Base(p) != want {
			return nil, fmt.Errorf("backup: database part %s is not %s: a part is missing", filepath.Base(p), want)
		}
	}
	target := filepath.Join(dir, filepath.FromSlash(DatabaseMember))
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("backup: joining the database parts: %w", err)
	}
	for _, p := range parts {
		data, err := os.ReadFile(p)
		if err == nil {
			_, err = out.Write(data)
		}
		if err != nil {
			out.Close()
			os.Remove(target)
			return nil, err
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(target)
		return nil, err
	}
	return parts, nil
}

// JoinRestoredDatabase joins a restored tree's database parts (in dataDir, the tree's data
// directory) into its database and removes them.
func JoinRestoredDatabase(dataDir string) error {
	parts, err := joinDatabase(filepath.Dir(dataDir))
	for _, p := range parts {
		if err == nil {
			err = os.Remove(p)
		}
	}
	return err
}

// isPart reports a database part member name.
func isPart(name string) bool { return strings.HasPrefix(name, partPrefix) }
