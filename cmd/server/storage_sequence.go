package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// runAdvanceStorageSequence is the explicit restore recovery required by KyIdentity's
// storage contract. Pollers never adopt a remotely returned sequence automatically.
func runAdvanceStorageSequence(args []string) error {
	fs := flag.NewFlagSet("advance-storage-sequence", flag.ContinueOnError)
	org := fs.String("org", "", "customer org ID (required)")
	past := fs.Int64("past", -1, "KyIdentity's latest reported sequence (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *org == "" || *past < 0 || *past == math.MaxInt64 || fs.NArg() != 0 {
		return errors.New("usage: kycalendar advance-storage-sequence -org <id> -past <remote-sequence>")
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
	if err := st.AdvanceStorageSequence(ctx, store.OrgID(*org), *past); err != nil {
		return err
	}
	fmt.Printf("Storage sequence for %s advanced past %d; the next successful report must grant new capacity.\n", *org, *past)
	return nil
}
