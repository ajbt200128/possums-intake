package siteauth

import (
	"context"
	"testing"

	"github.com/pocketbase/pocketbase/tools/types"
)

func TestFixtureKeyRetainsSDKAuthenticationAndValidation(t *testing.T) {
	f := newFixture(t)
	fixture := FixtureKeyAdapter{Adapter: f.adapter, Identity: fixtureIdentity}
	token, principal, err := fixture.Login(context.Background(), fixturePassword)
	if err != nil || token == "" || principal.RecordID != f.record.Id || principal.CollectionID != f.collection.Id {
		t.Fatal("SITE_TEST_FIXTURE_KEY_AUTH")
	}
	validated, err := fixture.Validate(context.Background(), token)
	if err != nil || validated != principal {
		t.Fatal("SITE_TEST_FIXTURE_KEY_VALIDATION")
	}
	badToken, badPrincipal, err := fixture.Login(context.Background(), "incorrect-synthetic-key")
	assertCode(t, err, "SITE_AUTH_REFUSED")
	if badToken != "" || badPrincipal != (Principal{}) {
		t.Fatal("SITE_TEST_FIXTURE_KEY_REFUSAL")
	}
	absent := FixtureKeyAdapter{Adapter: f.adapter, Identity: "absent@example.invalid"}
	_, _, err = absent.Login(context.Background(), fixturePassword)
	assertCode(t, err, "SITE_AUTH_REFUSED")

	f.collection.AuthRule = types.Pointer("verified = true")
	if f.app.Save(f.collection) != nil {
		t.Fatal("SITE_TEST_FIXTURE_RULE_SAVE")
	}
	_, _, err = fixture.Login(context.Background(), fixturePassword)
	assertCode(t, err, "SITE_AUTH_REFUSED")
	_, err = fixture.Validate(context.Background(), token)
	assertCode(t, err, "SITE_AUTH_INVALID")
}
