package siteauth_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

func TestCustomerSchemaLedgerRestart(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	if ledger.InitOperator(ctx, dir, ledger.OperatorCredentials{Email: "customer-restart@example.invalid", Password: "synthetic-restart-native-password"}) != nil {
		t.Fatal("restart fixture init unavailable")
	}
	s, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("restart fixture open unavailable")
	}
	defer func() {
		if s != nil && s.Close() != nil {
			t.Error("restart fixture close unavailable")
		}
	}()
	if _, err := s.Provision(ctx, "synthetic-persistent-account"); err != nil {
		t.Fatal("restart fixture account unavailable")
	}
	before, err := s.Balance(ctx, "synthetic-persistent-account")
	if err != nil {
		t.Fatal("restart baseline unavailable")
	}
	app, err := s.OperatorApp()
	if err != nil || siteauth.InitializeCustomerAuth(app) != nil {
		t.Fatal("restart auth initialization unavailable")
	}
	c, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("restart customer collection unavailable")
	}
	key := strings.Repeat("A", 128)
	lookup, secret := syntheticMaterial(key)
	r := core.NewRecord(c)
	r.Set("account_id", "synthetic-persistent-account")
	r.Set("key_lookup", lookup)
	r.SetPassword(secret)
	if app.Save(r) != nil {
		t.Fatal("restart synthetic credential unavailable")
	}
	a, err := siteauth.NewCustomer(app)
	if err != nil {
		t.Fatal("restart adapter unavailable")
	}
	token, _, err := a.Login(ctx, key)
	if err != nil {
		t.Fatal("restart initial login unavailable")
	}
	if s.Close() != nil {
		t.Fatal("restart first close unavailable")
	}
	s, err = ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("restart persisted ledger unavailable")
	}
	app, err = s.CustomerAuthApp()
	if err != nil {
		t.Fatal("restart persisted app unavailable")
	}
	a, err = siteauth.NewCustomer(app)
	if err != nil {
		t.Fatal("restart persisted auth schema unavailable")
	}
	p, err := a.Validate(ctx, token)
	if err != nil || p.AccountID != "synthetic-persistent-account" {
		t.Fatal("restart changed SDK signing or binding state")
	}
	if _, _, err := a.Login(ctx, key); err != nil {
		t.Fatal("restart credential no longer authenticates")
	}
	after, err := s.Balance(ctx, "synthetic-persistent-account")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("auth initialization/restart changed account snapshot")
	}
	assertNoKeyBytes(t, dir, key, secret)
}
