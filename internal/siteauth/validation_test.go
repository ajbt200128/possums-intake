package siteauth

import (
	"context"
	"errors"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
)

// Overrides only supported App boundaries; no private SDK internals or runtime
// hooks are replaced. Synthetic failures must not survive as raw causes.
type failingApp struct {
	core.App
	tokenFailure, ruleFailure, panicToken bool
}

func (a failingApp) FindAuthRecordByToken(token string, types ...string) (*core.Record, error) {
	if a.panicToken {
		panic(hostile)
	}
	if a.tokenFailure {
		return nil, errors.New(hostile)
	}
	return a.App.FindAuthRecordByToken(token, types...)
}
func (a failingApp) CanAccessRecord(record *core.Record, info *core.RequestInfo, rule *string) (bool, error) {
	if a.ruleFailure {
		return false, errors.New(hostile)
	}
	return a.App.CanAccessRecord(record, info, rule)
}
func TestValidateUnexpectedSDKFailuresStayClosed(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	for _, broken := range []failingApp{{App: f.app, tokenFailure: true}, {App: f.app, ruleFailure: true}, {App: f.app, panicToken: true}} {
		a, e := New(broken, f.collection.Name)
		if e != nil {
			t.Fatal("synthetic adapter setup unavailable")
		}
		p, e := a.Validate(context.Background(), token)
		if p != (Principal{}) {
			t.Fatal("SDK fault retained principal")
		}
		d := assertCode(t, e, "SITE_AUTH_UNAVAILABLE")
		if d.Kind() != failuretype.NativeAuthUnknown {
			t.Fatal("unexpected failure lost fallback")
		}
	}
}
func TestCancellationAfterSDKFinalResponseDoesNotReleaseToken(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.app.OnRecordAuthRequest(f.collection.Name).BindFunc(func(e *core.RecordAuthRequestEvent) error {
		result := e.Next()
		cancel()
		return result
	})
	token, p, e := f.adapter.Login(ctx, fixtureIdentity, fixturePassword)
	if token != "" || p != (Principal{}) {
		t.Fatal("interrupted dispatch released token")
	}
	assertCode(t, e, "SITE_AUTH_INTERRUPTED")
	assertQuiet(t, f)
}
