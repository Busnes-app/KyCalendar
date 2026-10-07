package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// KySignOnClient receives KyIdentity's signed directory webhooks. Sign-in is Provider's.
type KySignOnClient struct {
	config config.SSOConfig
	store  store.Store
}

func NewKySignOnClient(cfg config.SSOConfig, st store.Store) *KySignOnClient {
	return &KySignOnClient{config: cfg, store: st}
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
	// Only accounts bound to KyIdentity are its directory's: under any other binding a stale
	// secret would create or re-activate rows another provider's login could adopt.
	bound, err := k.store.Settings().GetSetting(ctx, KeyBound)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if kind, _, _ := strings.Cut(bound, " "); kind != KindKyIdentity {
		log.Printf("[SSO] directory webhook %q ignored: sign-in is not bound to KyIdentity", payload.Event)
		return nil
	}

	// The webhook only takes access away: it never sets an existing row active, so neither a stale
	// secret nor a write racing BindSignIn can restore a row the binding disabled. Every write is
	// column-scoped; the whole-row UpdateUser is never called here.
	users := k.store.Users()
	switch payload.Event {
	case "user.created", "user.updated":
		existing, err := k.findUser(ctx, payload.ID)
		if errors.Is(err, store.ErrNotFound) {
			status := "active"
			if payload.Status != "" && payload.Status != "active" {
				status = "inactive"
			}
			return users.CreateUser(ctx, &store.User{
				ID:          fmt.Sprintf("usr_%s", crypto.RandomHex(12)),
				Username:    payload.Username,
				Email:       payload.Email,
				DisplayName: payload.DisplayName,
				Role:        "user",
				Status:      status,
				SSOProvider: "kysignon",
				SSOSubject:  payload.ID,
			})
		}
		if err != nil {
			return err
		}
		// The admin grant comes from the `roles` claim at login and SCIM `roles`, never from
		// this webhook. It cannot see app roles, so an admin re-proves the role at the next
		// sign-in: their grants end here, before the profile write.
		deactivate := payload.Status != "" && payload.Status != "active"
		if deactivate || existing.Role == "admin" {
			if err := users.RevokeSSOUser(ctx, existing.ID, deactivate); err != nil {
				return err
			}
		}
		// SCIM owns a SCIM-provisioned user's profile.
		if existing.SSOProvider == "scim" {
			return nil
		}
		return users.UpdateKySignOnProfile(ctx, existing.ID, payload.DisplayName, payload.Email)

	case "user.deactivated", "user.deleted":
		existing, err := k.findUser(ctx, payload.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Deleting a user deletes their personal calendars; for a SCIM-owned user that is
		// SCIM's decision, so the webhook only takes their access away.
		if payload.Event == "user.deleted" && existing.SSOProvider != "scim" {
			return users.DeleteUser(ctx, existing.ID)
		}
		return users.RevokeSSOUser(ctx, existing.ID, true)
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
