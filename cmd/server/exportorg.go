package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calexport"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// runExportOrg writes one org's calendars for its account holder, on their request (tenancy spec,
// Lifecycle, Export): members export their own calendars, and this covers the rest of the org.
func runExportOrg(args []string) error {
	fs := flag.NewFlagSet("export-org", flag.ContinueOnError)
	org := fs.String("org", "", "ID of the org to export (required)")
	out := fs.String("out", "", "new directory to write the export into (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *org == "" || *out == "" || fs.NArg() > 0 {
		return errors.New("usage: kycalendar export-org -org <id> -out <dir>")
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	m, err := exportOrg(ctx, st, store.OrgID(*org), *out, time.Now().UTC())
	if err != nil {
		return err
	}
	log.Printf("✓ org %s exported to %s: %d calendars, %d items, %d copied as stored because they could not be merged (see manifest.json)", *org, *out, len(m.Calendars), m.Items, m.unmerged())
	return nil
}

// exportManifest is manifest.json: which file holds which calendar, by ID.
type exportManifest struct {
	OrgID      store.OrgID      `json:"org_id"`
	ExportedAt time.Time        `json:"exported_at"`
	Items      int              `json:"items"`
	Calendars  []exportCalendar `json:"calendars"`
}

func (m *exportManifest) unmerged() int {
	n := 0
	for _, c := range m.Calendars {
		n += len(c.Unmerged)
	}
	return n
}

type exportCalendar struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	OwnerKind  string   `json:"owner_kind"` // user or group
	OwnerID    string   `json:"owner_id"`
	Username   string   `json:"username,omitempty"`
	Email      string   `json:"email,omitempty"`
	Components []string `json:"components"`
	Subscribed bool     `json:"subscribed,omitempty"`
	File       string   `json:"file"`
	Items      int      `json:"items"`
	// Unmerged are stored objects that could not go into File (they do not parse, or go-ical cannot
	// write them back), each copied byte for byte into its own file.
	Unmerged []unmergedCopy `json:"unmerged,omitempty"`
}

// unmergedCopy is one object copied as stored: its file and the object name it was stored under.
type unmergedCopy struct {
	File string `json:"file"`
	Name string `json:"name"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// exportOrg writes org's calendars under dir, which must not exist yet: one .ics per calendar,
// in a folder per member (members/<user ID>/) or groups/, and manifest.json. Every file is 0600
// and every folder 0700, since they hold calendar content. Nothing stored is ever left out: an
// object that cannot be merged into its calendar's file is copied as stored next to it
// (<calendar ID>.unmerged/<object name>) and listed in the manifest, so an export handed over
// before a purge is never short and no single object can stop it.
func exportOrg(ctx context.Context, st store.Store, org store.OrgID, dir string, now time.Time) (*exportManifest, error) {
	if _, err := st.OrgQuotas(ctx, org); errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no org %s here", org)
	} else if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the output directory must not exist yet: %s", dir)
	}
	// Written into a private sibling and renamed into place only when complete, so a failed
	// export leaves nothing that looks like one and a retry can use the same -out.
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".partial-")
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(tmp)
		}
	}()
	cals, err := st.Org(org).ListCalendars(ctx)
	if err != nil {
		return nil, err
	}
	m := &exportManifest{OrgID: org, ExportedAt: now, Calendars: []exportCalendar{}}
	// Names are made safe by replacing characters, so two can collide (a b.ics, a_b.ics): every
	// path gets a numbered suffix until it is unused, and nothing is written over.
	used := map[string]bool{"manifest.json": true}
	unique := func(path string) string {
		ext := filepath.Ext(path)
		stem := strings.TrimSuffix(path, ext)
		p := path
		for n := 2; used[strings.ToLower(p)]; n++ {
			p = fmt.Sprintf("%s~%d%s", stem, n, ext)
		}
		used[strings.ToLower(p)] = true
		return p
	}
	for _, c := range cals {
		e := exportCalendar{ID: c.ID, Name: c.Name, OwnerKind: c.OwnerKind, OwnerID: c.OwnerID, Components: c.Components, Subscribed: c.Subscribed}
		folder := "groups"
		if c.OwnerKind == "user" {
			u, err := st.Org(org).GetUser(ctx, c.OwnerID)
			if err != nil {
				return nil, fmt.Errorf("owner of calendar %s: %w", c.ID, err)
			}
			e.Username, e.Email = u.Username, u.Email
			folder = filepath.Join("members", unsafeName.ReplaceAllString(u.ID, "_"))
		}
		e.File = unique(filepath.ToSlash(filepath.Join(folder, unsafeName.ReplaceAllString(c.ID, "_")+".ics")))
		body, items, unmerged, err := calexport.CalendarICS(ctx, st, c.ID)
		e.Items = items
		if err != nil {
			return nil, fmt.Errorf("calendar %s: %w", c.ID, err)
		}
		if err := writeExport(filepath.Join(tmp, filepath.FromSlash(e.File)), body); err != nil {
			return nil, err
		}
		for _, o := range unmerged {
			f := unique(strings.TrimSuffix(e.File, ".ics") + ".unmerged/" + unsafeName.ReplaceAllString(o.Name, "_"))
			if err := writeExport(filepath.Join(tmp, filepath.FromSlash(f)), o.Data); err != nil {
				return nil, err
			}
			e.Unmerged = append(e.Unmerged, unmergedCopy{File: f, Name: o.Name})
		}
		m.Items += e.Items
		m.Calendars = append(m.Calendars, e)
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeExport(filepath.Join(tmp, "manifest.json"), append(manifest, '\n')); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	done = true
	return m, nil
}

// writeExport writes one export file; a test replaces it to fail a write.
var writeExport = writePrivate

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
