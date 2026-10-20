package siteauth

import "context"

// FixtureKeyAdapter binds the single public demo key to a fixed synthetic SDK
// identity. It is not a production key lookup, provisioning or storage scheme.
// The supported SDK still verifies the key as the fixture account's password.
type FixtureKeyAdapter struct {
	Adapter  *Adapter
	Identity string
}

func (a FixtureKeyAdapter) Login(ctx context.Context, key string) (string, Principal, error) {
	return a.Adapter.Login(ctx, a.Identity, key)
}

func (a FixtureKeyAdapter) Validate(ctx context.Context, token string) (Principal, error) {
	return a.Adapter.Validate(ctx, token)
}
