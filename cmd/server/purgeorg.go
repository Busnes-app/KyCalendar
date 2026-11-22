package main

import (
	"context"
	"errors"
	"flag"
	"log"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// runPurgeOrg deletes a stale org's data after staff confirmed in KyIdentity that the org is
// gone (tenancy spec, Lifecycle, Restore). A poll never purges an org on absence alone.
func runPurgeOrg(args []string) error {
	fs := flag.NewFlagSet("purge-org", flag.ContinueOnError)
	org := fs.String("org", "", "ID of the stale or deleted org to purge (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *org == "" || fs.NArg() > 0 {
		return errors.New("usage: kycalendar purge-org -org <id>")
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
	if err := purgeOrg(ctx, st, store.OrgID(*org)); err != nil {
		return err
	}
	log.Printf("✓ org %s purged; its row stays as a tombstone", *org)
	return nil
}

// purgeOrg tombstones org if it is stale or deleted, then purges it. Multi-tenant installs only.
func purgeOrg(ctx context.Context, st store.Store, org store.OrgID) error {
	ten, err := st.Tenancy(ctx)
	if err != nil {
		return err
	}
	if !ten.Multi {
		return errors.New("a single-tenant install has one org; nothing to purge")
	}
	if err := st.TombstoneOrg(ctx, ten.Staff, org); err != nil {
		return err
	}
	return st.PurgeOrg(ctx, ten.Staff, org)
}
