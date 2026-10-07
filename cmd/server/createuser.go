package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var errUserExists = errors.New("a user with that name already exists")

// runCreateUser creates a local everyday account. The initial password is read from stdin so it
// never appears in the process list; the user must replace it at first sign-in.
func runCreateUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	username := fs.String("username", "", "username (required)")
	_ = fs.Parse(args)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		log.Fatal("Error: write the initial password to stdin")
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
	if err := createUser(ctx, st, *username, strings.TrimRight(line, "\r\n")); err != nil {
		log.Fatalf("Failed to create user: %v", err)
	}
	log.Printf("✓ User %q created; they must change the password at first sign-in", *username)
}

func createUser(ctx context.Context, st store.Store, username, pw string) error {
	if err := auth.ValidateUsername(username); err != nil {
		return err
	}
	if err := auth.ValidatePassword(pw); err != nil {
		return err
	}
	if _, err := st.Users().GetUserByUsername(ctx, username); err == nil {
		return errUserExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	return st.Users().CreateUser(ctx, &store.User{
		ID: fmt.Sprintf("usr_%s", crypto.RandomHex(12)), Username: username, DisplayName: username,
		PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local", MustChangePassword: true,
	})
}
