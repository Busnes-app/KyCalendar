package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var (
	errNoSuchUser = errors.New("no account with exactly that name")
	errNotLocal   = errors.New("account signs in through SSO and has no local password")
)

// runResetPassword is the operator reset for any local account. The temporary password is read
// from stdin; the role and status are kept, and the user must replace it at the next sign-in.
func runResetPassword(args []string) {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	username := fs.String("username", "", "username (required)")
	_ = fs.Parse(args)
	if *username == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "Usage: kycalendar reset-password -username <name> (temporary password on stdin)")
		os.Exit(2)
	}
	pw, err := readPasswordLine(os.Stdin)
	if err != nil {
		log.Fatal("Error: write the temporary password to stdin")
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
	if err := resetPassword(ctx, st, *username, pw); err != nil {
		log.Fatalf("Failed to reset %q: %v", *username, err)
	}
	log.Printf("✓ Password for %q reset; sessions and app passwords revoked; they must change it at next sign-in", *username)
}

func resetPassword(ctx context.Context, st store.Store, username, pw string) error {
	if err := auth.ValidatePassword(pw); err != nil {
		return err
	}
	u, err := st.Users().GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return errNoSuchUser
	} else if err != nil {
		return err
	}
	// The lookup is case-insensitive; a reset hands out access, so only the exact name counts.
	if u.Username != username {
		return errNoSuchUser
	}
	if u.SSOProvider != "local" {
		return errNotLocal
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	return st.Users().ResetPassword(ctx, store.System, u.ID, hash)
}
