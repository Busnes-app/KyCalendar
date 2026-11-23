package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// runExportOrg writes one org's calendars for its account holder, on their request (tenancy spec,
// Lifecycle, Deleted): during deletion_scheduled nobody in the org can reach KyCalendar.
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
	log.Printf("✓ org %s exported to %s: %d calendars, %d items", *org, *out, len(m.Calendars), m.Items)
	return nil
}

// exportManifest is manifest.json: which file holds which calendar, by ID.
type exportManifest struct {
	OrgID      store.OrgID      `json:"org_id"`
	ExportedAt time.Time        `json:"exported_at"`
	Items      int              `json:"items"`
	Calendars  []exportCalendar `json:"calendars"`
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
	Unreadable []string `json:"unreadable,omitempty"` // stored objects that do not parse, by name
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// exportOrg writes org's calendars under dir, which must not exist yet: one .ics per calendar,
// in a folder per member (members/<user ID>/) or groups/, and manifest.json. Every file is 0600
// and every folder 0700, since they hold calendar content.
func exportOrg(ctx context.Context, st store.Store, org store.OrgID, dir string, now time.Time) (*exportManifest, error) {
	if _, err := st.OrgQuotas(ctx, org); errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no org %s here", org)
	} else if err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the output directory must not exist yet: %w", err)
	}
	cals, err := st.Org(org).ListCalendars(ctx)
	if err != nil {
		return nil, err
	}
	m := &exportManifest{OrgID: org, ExportedAt: now, Calendars: []exportCalendar{}}
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
		e.File = filepath.ToSlash(filepath.Join(folder, unsafeName.ReplaceAllString(c.ID, "_")+".ics"))
		body, err := exportCalendarICS(ctx, st, c, &e)
		if err != nil {
			return nil, fmt.Errorf("calendar %s: %w", c.ID, err)
		}
		if err := writePrivate(filepath.Join(dir, filepath.FromSlash(e.File)), body); err != nil {
			return nil, err
		}
		m.Items += e.Items
		m.Calendars = append(m.Calendars, e)
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return m, writePrivate(filepath.Join(dir, "manifest.json"), append(manifest, '\n'))
}

// exportCalendarICS is one VCALENDAR holding every stored object of c, each time zone once.
func exportCalendarICS(ctx context.Context, st store.Store, c *store.Calendar, e *exportCalendar) ([]byte, error) {
	objs, err := st.Calendars().ListObjects(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//Busnes-app//KyCalendar export-org//EN")
	zones := map[string]bool{}
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			e.Unreadable = append(e.Unreadable, o.Name)
			continue
		}
		for _, child := range cal.Children {
			if child.Name == ical.CompTimezone {
				id, _ := child.Props.Text(ical.PropTimezoneID)
				if zones[id] {
					continue
				}
				zones[id] = true
			}
			out.Children = append(out.Children, child)
		}
		e.Items++
	}
	var b bytes.Buffer
	if len(out.Children) == 0 {
		// go-ical refuses a calendar with no components; an empty calendar is still exported.
		b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Busnes-app//KyCalendar export-org//EN\r\nEND:VCALENDAR\r\n")
		return b.Bytes(), nil
	}
	if err := ical.NewEncoder(&b).Encode(out); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
