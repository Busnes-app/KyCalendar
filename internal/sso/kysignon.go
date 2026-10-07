package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// KySignOnClient manages interactions with the central KySignOn identity provider.
type KySignOnClient struct {
	config config.SSOConfig
	store  store.Store
	flow   *oauthFlow
}

func NewKySignOnClient(cfg config.SSOConfig, st store.Store) *KySignOnClient {
	return &KySignOnClient{
		config: cfg,
		store:  st,
		flow:   newOAuthFlow(cfg.KySignOnIssuer, cfg.KySignOnClientID, cfg.KySignOnSecret),
	}
}

// BuildAuthURL generates the authorization code URL with PKCE for KySignOn.
func (k *KySignOnClient) BuildAuthURL(ctx context.Context, redirectURI, state, verifier, nonce string) (string, error) {
	return k.flow.authCodeURL(ctx, redirectURI, state, verifier, nonce)
}

// ExchangeCode exchanges the authorization code and verifier for identity claims.
func (k *KySignOnClient) ExchangeCode(ctx context.Context, code, verifier, redirectURI, expectedNonce string) (*IdentityClaims, error) {
	claims, err := k.flow.exchange(ctx, code, verifier, redirectURI, expectedNonce)
	if err != nil {
		return nil, err
	}
	claims.Provider = "kysignon"
	return claims, nil
}

// KySignOnSyncPayload defines the schema received during automatic directory replication webhooks.
type KySignOnSyncPayload struct {
	Event       string `json:"event"` // "user.created", "user.updated", "user.deactivated", "user.deleted"
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	Timestamp   int64  `json:"timestamp"`
}

// HandleSyncWebhook processes inbound HMAC-SHA256 signed user updates from KySignOn server.
func (k *KySignOnClient) HandleSyncWebhook(ctx context.Context, body []byte, signature string) error {
	if k.config.KySignOnHMACSecret == "" {
		return errors.New("webhook HMAC secret is not configured")
	}

	if !crypto.VerifyHMACSHA256(body, k.config.KySignOnHMACSecret, signature) {
		return errors.New("invalid webhook signature")
	}

	var payload KySignOnSyncPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("invalid json payload: %w", err)
	}
	if payload.Timestamp == 0 || time.Since(time.Unix(payload.Timestamp, 0)).Abs() > 5*time.Minute {
		return errors.New("webhook timestamp is missing or expired")
	}
	if payload.ID == "" {
		return errors.New("webhook user id is missing")
	}

	switch payload.Event {
	case "user.created", "user.updated":
		existing, err := k.findUser(ctx, payload.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}

		status := payload.Status
		if status == "" {
			status = "active"
		}

		// The admin grant comes from the `roles` claim at login and SCIM `roles`, never from
		// this webhook, whose legacy `role` is KyIdentity's global role. The webhook cannot see
		// app roles, so an admin re-proves the role at the next sign-in: their sessions end here.
		if existing != nil {
			if existing.Status != status || existing.Role == "admin" {
				if err := k.revoke(ctx, existing.ID); err != nil {
					return err
				}
			}
			existing.Username = payload.Username
			existing.Email = payload.Email
			existing.DisplayName = payload.DisplayName
			existing.Status = status
			return k.store.Users().UpdateUser(ctx, existing)
		}

		newUser := &store.User{
			ID:          fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
			Username:    payload.Username,
			Email:       payload.Email,
			DisplayName: payload.DisplayName,
			Role:        "user",
			Status:      status,
			SSOProvider: "kysignon",
			SSOSubject:  payload.ID,
		}
		return k.store.Users().CreateUser(ctx, newUser)

	case "user.deactivated":
		existing, err := k.findUser(ctx, payload.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := k.revoke(ctx, existing.ID); err != nil {
			return err
		}
		existing.Status = "inactive"
		return k.store.Users().UpdateUser(ctx, existing)

	case "user.deleted":
		existing, err := k.findUser(ctx, payload.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return k.store.Users().DeleteUser(ctx, existing.ID)
	}

	return nil
}

// findUser resolves a directory ID to the local user. KyIdentity's SCIM externalId is the same
// ID, so a SCIM-provisioned user is found too.
func (k *KySignOnClient) findUser(ctx context.Context, id string) (*store.User, error) {
	u, err := k.store.Users().GetUserBySSO(ctx, "kysignon", id)
	if errors.Is(err, store.ErrNotFound) {
		return k.store.Users().GetUserBySSO(ctx, "scim", id)
	}
	return u, err
}

// revoke ends the user's sessions and app passwords; callers do it before storing the change,
// so a failed write still leaves the user signed out.
func (k *KySignOnClient) revoke(ctx context.Context, userID string) error {
	if err := k.store.Sessions().DeleteUserSessions(ctx, userID); err != nil {
		return err
	}
	return k.store.AppPasswords().DeleteByUser(ctx, userID)
}
