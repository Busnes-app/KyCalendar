package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	// write them back), each copied byte for byte into its own file, by path.
	Unmerged []string `json:"unmerged,omitempty"`
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
		body, unmerged, err := exportCalendarICS(ctx, st, c, &e)
		if err != nil {
			return nil, fmt.Errorf("calendar %s: %w", c.ID, err)
		}
		if err := writeExport(filepath.Join(tmp, filepath.FromSlash(e.File)), body); err != nil {
			return nil, err
		}
		for _, o := range unmerged {
			f := strings.TrimSuffix(e.File, ".ics") + ".unmerged/" + unsafeName.ReplaceAllString(o.Name, "_")
			if err := writeExport(filepath.Join(tmp, filepath.FromSlash(f)), o.Data); err != nil {
				return nil, err
			}
			e.Unmerged = append(e.Unmerged, f)
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

// exportCalendarICS is one VCALENDAR holding every stored object of c, each time zone once, and
// the objects it could not merge: those that do not parse or that go-ical cannot write back.
func exportCalendarICS(ctx context.Context, st store.Store, c *store.Calendar, e *exportCalendar) ([]byte, []*store.CalendarObject, error) {
	objs, err := st.Calendars().ListObjects(ctx, c.ID)
	if err != nil {
		return nil, nil, err
	}
	var unmerged []*store.CalendarObject
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//Busnes-app//KyCalendar export-org//EN")
	zones := map[string][]byte{} // TZID -> the encoded VTIMEZONE written under it
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err == nil {
			err = ical.NewEncoder(io.Discard).Encode(cal) // what is merged must write back
		}
		if err != nil {
			unmerged = append(unmerged, o)
			continue
		}
		renamed := map[string]string{}
		var items []*ical.Component
		for _, child := range cal.Children {
			if child.Name != ical.CompTimezone {
				items = append(items, child)
				continue
			}
			id, _ := child.Props.Text(ical.PropTimezoneID)
			body, err := zoneBytes(child)
			if err != nil {
				return nil, nil, err // the whole object encoded, so its zone does too
			}
			// One TZID may name different rules in different objects (Outlook's "Customized Time
			// Zone"): only an identical definition is shared; another gets a TZID of its own.
			name := id
			for n := 2; zones[name] != nil && !bytes.Equal(zones[name], body); n++ {
				name = fmt.Sprintf("%s (%d)", id, n)
			}
			if name != id {
				renamed[id] = name // set before sharing, so a reused renamed zone is still pointed at
			}
			if zones[name] != nil {
				continue // this exact definition is already written under name
			}
			child.Props.SetText(ical.PropTimezoneID, name)
			zones[name] = body
			out.Children = append(out.Children, child)
		}
		for _, c := range items {
			renameTZID(c, renamed)
		}
		out.Children = append(out.Children, items...)
		e.Items++
	}
	var b bytes.Buffer
	if len(out.Children) == 0 {
		// go-ical refuses a calendar with no components; an empty calendar is still exported.
		b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Busnes-app//KyCalendar export-org//EN\r\nEND:VCALENDAR\r\n")
		return b.Bytes(), unmerged, nil
	}
	if err := ical.NewEncoder(&b).Encode(out); err != nil {
		return nil, nil, err
	}
	return b.Bytes(), unmerged, nil
}

// zoneBytes is z encoded on its own, to compare two definitions.
func zoneBytes(z *ical.Component) ([]byte, error) {
	var b bytes.Buffer
	err := ical.NewEncoder(&b).Encode(&ical.Calendar{Component: &ical.Component{Name: ical.CompCalendar,
		Props:    ical.Props{ical.PropVersion: {{Name: ical.PropVersion, Value: "2.0"}}, ical.PropProductID: {{Name: ical.PropProductID, Value: "x"}}},
		Children: []*ical.Component{z}}})
	return b.Bytes(), err
}

// renameTZID points every TZID parameter in c and its children at the renamed zones.
func renameTZID(c *ical.Component, renamed map[string]string) {
	if len(renamed) == 0 {
		return
	}
	for _, ps := range c.Props {
		for i := range ps {
			if to, ok := renamed[ps[i].Params.Get(ical.ParamTimezoneID)]; ok {
				ps[i].Params.Set(ical.ParamTimezoneID, to)
			}
		}
	}
	for _, child := range c.Children {
		renameTZID(child, renamed)
	}
}

// writeExport writes one export file; a test replaces it to fail a write.
var writeExport = writePrivate

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
