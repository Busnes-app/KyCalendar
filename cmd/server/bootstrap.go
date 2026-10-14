package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// initialAdminPasswordFile holds the generated bootstrap password, never the log.
const initialAdminPasswordFile = "initial-admin-password"

// bootstrapAdmin creates the "admin" account on an empty database. Its password is
// KY_ADMIN_PASSWORD, or one generated into <dataDir>/initial-admin-password (0600). A file
// left by a start that died before the account was written is reused, so a retry never
// strands a password nobody can read.
func bootstrapAdmin(ctx context.Context, st store.Store, dataDir string) error {
	count, err := st.Users().CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count != 0 {
		return nil
	}
	adminPass := os.Getenv("KY_ADMIN_PASSWORD")
	if adminPass == "" {
		path := filepath.Join(dataDir, initialAdminPasswordFile)
		if adminPass, err = initialAdminPassword(dataDir, path); err != nil {
			return err
		}
		log.Printf("[SECURITY] Initial bootstrap: admin account \"admin\"; its one-time password is in %s (change it at first sign-in, then delete the file)", path)
	}
	hash, err := password.Hash(adminPass)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	return st.Users().CreateUser(ctx, &store.User{
		ID:                 fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
		Username:           "admin",
		DisplayName:        "Administrator",
		PasswordHash:       hash,
		Role:               "admin",
		Status:             "active",
		SSOProvider:        "local",
		MustChangePassword: true,
	})
}

// initialAdminPassword creates path exclusively with a fresh password, or returns the one
// already in it.
func initialAdminPassword(dataDir, path string) (string, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		raw, err := os.ReadFile(path)
		if pw := strings.TrimSpace(string(raw)); err == nil && pw != "" {
			return pw, nil
		}
		return "", fmt.Errorf("%s exists but holds no password; remove it and restart", path)
	}
	if err != nil {
		return "", err
	}
	pw := crypto.RandomHex(12)
	if _, err := f.WriteString(pw + "\n"); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return pw, nil
}
