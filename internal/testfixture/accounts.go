// Package testfixture creates public synthetic SDK accounts for isolated tests.
// No application runtime imports this package. Fixed IDs, where supplied, are
// the real stored SDK record IDs, never aliases for a second wallet identity.
package testfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/pocketbase/pocketbase/core"
)

const Password = "public-synthetic-sdk-password-only"

type AccountApp interface{ CustomerAuthApp() (core.App, error) }

func Create(ctx context.Context, service AccountApp, id string) (*core.Record, error) {
	app, err := service.CustomerAuthApp()
	if err != nil {
		return nil, err
	}
	return CreateRecord(ctx, app, id, "0", true)
}

func CreateRecord(ctx context.Context, app core.App, id, balance string, enabled bool) (*core.Record, error) {
	c, err := app.FindCollectionByNameOrId("accounts")
	if err != nil {
		return nil, err
	}
	r := core.NewRecord(c)
	if id != "" {
		r.Id = id
	}
	// A deterministic public lookup per actual fixture record avoids credential
	// collisions; real customer key derivation is exercised by siteauth tests.
	sum := sha256.Sum256([]byte("public-test-fixture/" + r.Id))
	r.Set("key_lookup", "v1_"+hex.EncodeToString(sum[:]))
	r.Set("balance", balance)
	r.Set("enabled", enabled)
	r.SetPassword(Password)
	if err := app.SaveWithContext(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}
