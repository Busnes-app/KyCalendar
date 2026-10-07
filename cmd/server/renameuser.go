package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// runRenameUser renames a local account, e.g. a break-glass admin whose name an IdP user needs.
// Password, role, MFA and sessions are kept; CalDAV clients must sign in with the new name.
func runRenameUser(args []string) {
	fs := flag.NewFlagSet("rename-user", flag.ExitOnError)
	from := fs.String("username", "", "current username (exact)")
	to := fs.String("to", "", "new username")
	_ = fs.Parse(args)
	if *from == "" || *to == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "Usage: kycalendar rename-user -username <current> -to <new>")
		os.Exit(2)
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("DB error: %v", err)
	}
	defer st.Close()
	if err := renameUser(ctx, st, *from, *to); err != nil {
		log.Fatalf("Failed to rename %q: %v", *from, err)
	}
	log.Printf("✓ %q renamed to %q", *from, *to)
}

func renameUser(ctx context.Context, st store.Store, from, to string) error {
	if err := auth.ValidateUsername(to); err != nil {
		return err
	}
	u, err := st.Users().GetUserByUsername(ctx, from)
	if errors.Is(err, store.ErrNotFound) {
		return errNoSuchUser
	} else if err != nil {
		return err
	}
	if u.Username != from {
		return errNoSuchUser
	}
	if u.SSOProvider != "local" {
		return errNotLocal
	}
	// The lookup is case-insensitive, so this also refuses a confusable twin of an existing name.
	if _, err := st.Users().GetUserByUsername(ctx, to); err == nil {
		return errUserExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := st.Users().RenameUser(ctx, "system", u.ID, to); errors.Is(err, store.ErrAlreadyExists) {
		return errUserExists
	} else {
		return err
	}
}
