package siteauth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"possums-intake/internal/siteauth"
)

func TestCustomerAdapterCurrentBindingAndSDKAuthority(t *testing.T) {
	f := newKeyFixture(t)
	key := strings.Repeat("A", 128)
	created := f.request(t, "POST", "/api/collections/accounts/records", map[string]any{"password": key, "passwordConfirm": key})
	if created.Code != 200 {
		t.Fatal("native customer provision unavailable")
	}
	a, err := siteauth.NewCustomer(f.app)
	if err != nil {
		t.Fatal("customer adapter setup unavailable")
	}
	token, p, err := a.Login(context.Background(), key)
	if err != nil || token == "" || p.AccountID != p.RecordID || len(p.RecordID) != 15 || p.CollectionID != siteauth.CustomerCollectionID {
		t.Fatal("customer SDK binding unavailable")
	}
	r, err := f.app.FindRecordById(siteauth.CustomerCollection, p.RecordID)
	if err != nil {
		t.Fatal("customer record unavailable")
	}
	r.Set("balance", "17")
	if f.app.Save(r) != nil {
		t.Fatal("synthetic binding update unavailable")
	}
	current, err := a.Validate(context.Background(), token)
	if err != nil || current.AccountID != r.Id || current.RecordID != r.Id {
		t.Fatal("adapter returned a non-record account identity")
	}
	c, err := f.app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("customer settings unavailable")
	}
	c.AuthRule = types.Pointer("id = 'no-synthetic-record'")
	if f.app.Save(c) != nil {
		t.Fatal("customer rule update unavailable")
	}
	if _, err := a.Validate(context.Background(), token); err == nil {
		t.Fatal("customer AuthRule bypassed")
	}
	c.AuthRule = types.Pointer("")
	c.MFA.Enabled = true
	c.OTP.Enabled = true
	if f.app.Save(c) != nil {
		t.Fatal("customer MFA update unavailable")
	}
	if _, _, err := a.Login(context.Background(), key); err == nil {
		t.Fatal("unsupported MFA granted customer auth")
	}
	if _, err := a.Validate(context.Background(), token); err == nil {
		t.Fatal("unsupported MFA preserved customer access")
	}
	var challenges int
	if f.app.DB().NewQuery("SELECT count(*) FROM _mfas").Row(&challenges) != nil || challenges != 0 || !c.MFA.Enabled || !c.OTP.Enabled {
		t.Fatal("website created challenges or disabled native MFA/OTP")
	}
}

func TestCustomerSchemaInitializationPreservesExistingCredentials(t *testing.T) {
	f := newKeyFixture(t)
	key := strings.Repeat("A", 43)
	response := f.request(t, "POST", "/api/collections/accounts/records", map[string]any{"password": key, "passwordConfirm": key})
	if response.Code != 200 {
		t.Fatal("synthetic provision unavailable")
	}
	lookup, _ := syntheticMaterial(key)
	r, err := f.app.FindFirstRecordByData(siteauth.CustomerCollection, "key_lookup", lookup)
	if err != nil {
		t.Fatal("credential unavailable")
	}
	beforeHash, beforeToken := r.GetString("password:hash"), r.TokenKey()
	c, err := f.app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil || c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil || c.ManageRule != nil {
		t.Fatal("initial customer rules not SDK-superuser-only")
	}
	secret, updated := c.AuthToken.Secret, c.Updated
	if siteauth.InitializeCustomerAuth(f.app) != nil {
		t.Fatal("compatible repeated initialization unavailable")
	}
	r, err = f.app.FindRecordById(siteauth.CustomerCollection, r.Id)
	if err != nil || r.GetString("password:hash") != beforeHash || r.TokenKey() != beforeToken {
		t.Fatal("schema initialization reset credential")
	}
	c, err = f.app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil || c.AuthToken.Secret != secret || c.Updated != updated {
		t.Fatal("schema initialization rewrote SDK auth state")
	}
	c.Fields.Add(&core.TextField{Name: "unreviewed_field"})
	if f.app.Save(c) != nil {
		t.Fatal("synthetic schema mutation unavailable")
	}
	if siteauth.InitializeCustomerAuth(f.app) == nil {
		t.Fatal("incompatible schema silently repaired")
	}
	if _, err := siteauth.NewCustomer(f.app); err == nil {
		t.Fatal("incompatible customer schema admitted")
	}
	var count int
	if f.app.DB().NewQuery("SELECT count(*) FROM accounts").Row(&count) != nil || count != 1 {
		t.Fatal("incompatible schema refusal deleted credential")
	}
}
