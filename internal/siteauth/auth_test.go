package siteauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const fixtureIdentity = "synthetic@example.invalid"
const fixturePassword = "synthetic-password-only-123"
const hostile = "SYNTHETIC_SECRET_EXCEPTION_SENTINEL"

type fixture struct {
	app        *core.BaseApp
	adapter    *Adapter
	collection *core.Collection
	record     *core.Record
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), QueryTimeout: time.Second})
	app.Settings().Logs.MaxDays = 0
	if app.Bootstrap() != nil {
		t.Fatal("synthetic bootstrap unavailable")
	}
	t.Cleanup(func() {
		if app.ResetBootstrapState() != nil {
			t.Error("synthetic fixture cleanup unavailable")
		}
	})
	app.Settings().Logs.MaxDays = 0
	if app.Save(app.Settings()) != nil {
		t.Fatal("quiet fixture settings unavailable")
	}
	c := core.NewAuthCollection("synthetic_customers")
	c.AuthAlert.Enabled = false
	c.PasswordAuth.Enabled = true
	c.PasswordAuth.IdentityFields = []string{"email"}
	c.AuthRule = types.Pointer("")
	if app.Save(c) != nil {
		t.Fatal("synthetic collection unavailable")
	}
	// All five public CRUD rules remain nil; no production migration/provisioning.
	if c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
		t.Fatal("fixture public CRUD opened")
	}
	record := core.NewRecord(c)
	record.SetEmail(fixtureIdentity)
	record.SetPassword(fixturePassword)
	if app.Save(record) != nil {
		t.Fatal("synthetic credential unavailable")
	}
	a, e := New(app, c.Name)
	if e != nil {
		t.Fatal("adapter setup unavailable")
	}
	return &fixture{app, a, c, record}
}
func assertCode(t *testing.T, e error, code string) *ledger.Diagnostic {
	t.Helper()
	var d *ledger.Diagnostic
	if !errors.As(e, &d) || string(d.Code) != code || d.Stage == "" || d.Constraint == "" || d.NextStep == "" || !d.Kind().Valid() {
		if d != nil {
			t.Fatalf("closed diagnostic mismatch: code=%s stage=%s constraint=%s expected=%s", d.Code, d.Stage, d.Constraint, code)
		}
		t.Fatal("missing expected closed diagnostic")
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), hostile) || strings.Contains(string(b), fixtureIdentity) || strings.Contains(string(b), fixturePassword) {
		t.Fatal("private data in diagnostic")
	}
	return d
}
func (f *fixture) login(t *testing.T) string {
	t.Helper()
	token, p, e := f.adapter.Login(context.Background(), fixtureIdentity, fixturePassword)
	if e != nil || token == "" || p.RecordID != f.record.Id || p.CollectionID != f.collection.Id {
		t.Fatal("final SDK authentication unavailable")
	}
	return token
}
func TestSDKFinalPasswordAndNeutralRefusal(t *testing.T) {
	f := newFixture(t)
	token := f.login(t)
	if _, e := f.adapter.Validate(context.Background(), token); e != nil {
		t.Fatal("valid credential refused")
	}
	var prior *ledger.Diagnostic
	for _, identity := range []string{fixtureIdentity, "absent@example.invalid"} {
		token, p, e := f.adapter.Login(context.Background(), identity, "incorrect-password")
		d := assertCode(t, e, "SITE_AUTH_REFUSED")
		if token != "" || p != (Principal{}) || d.Kind() != failuretype.AuthenticationRefused {
			t.Fatal("refusal released private authentication")
		}
		if prior != nil && *prior != *d {
			t.Fatal("absent and wrong-secret diagnostics differ")
		}
		prior = d
	}
	assertQuiet(t, f)
}
func assertQuiet(t *testing.T, f *fixture) {
	t.Helper()
	var count int
	if f.app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
		t.Fatal("fixture SDK logs not empty")
	}
	if f.app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count) != nil || count != 0 {
		t.Fatal("fixture auth origins not empty")
	}
}
func TestSDKMFAChallengeIsNotFinalAuthentication(t *testing.T) {
	f := newFixture(t)
	f.collection.MFA.Enabled = true
	f.collection.MFA.Rule = ""
	f.collection.OTP.Enabled = true
	if f.app.Save(f.collection) != nil {
		t.Fatal("MFA configuration unavailable")
	}
	seen := false
	f.app.OnRecordAuthRequest(f.collection.Name).BindFunc(func(e *core.RecordAuthRequestEvent) error { seen = e.Token != ""; return e.Next() })
	token, p, e := f.adapter.Login(context.Background(), fixtureIdentity, fixturePassword)
	assertCode(t, e, "SITE_AUTH_MFA_UNSUPPORTED")
	if !seen || token != "" || p != (Principal{}) {
		t.Fatal("pre-MFA hook mistaken for completion")
	}
	var count int
	if f.app.DB().NewQuery("SELECT count(*) FROM _mfas").Row(&count) != nil || count != 1 {
		t.Fatal("actual SDK MFA challenge not exercised")
	}
	if !f.collection.OTP.Enabled || !f.collection.MFA.Enabled {
		t.Fatal("native MFA/OTP disabled")
	}
	assertQuiet(t, f)
}
func TestCredentialAndRuleRevalidation(t *testing.T) {
	for _, change := range []string{"delete", "password", "record_token_key", "collection_token_key", "auth_rule", "auth_rule_nil", "auth_rule_request_info"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			token := f.login(t)
			switch change {
			case "delete":
				if f.app.Delete(f.record) != nil {
					t.Fatal("fixture delete unavailable")
				}
			case "password":
				f.record.SetPassword("different-synthetic-password-456")
				if f.app.Save(f.record) != nil {
					t.Fatal("fixture password change unavailable")
				}
			case "record_token_key":
				f.record.Set("tokenKey", "different-synthetic-token-key-12345")
				if f.app.Save(f.record) != nil {
					t.Fatal("fixture token change unavailable")
				}
			case "collection_token_key":
				f.collection.AuthToken.Secret = "different-synthetic-secret-1234567890"
				if f.app.Save(f.collection) != nil {
					t.Fatal("fixture collection key change unavailable")
				}
			case "auth_rule":
				f.collection.AuthRule = types.Pointer("verified = true")
				if f.app.Save(f.collection) != nil {
					t.Fatal("fixture rule change unavailable")
				}
			case "auth_rule_nil":
				f.collection.AuthRule = nil
				if f.app.Save(f.collection) != nil {
					t.Fatal("fixture rule change unavailable")
				}
			case "auth_rule_request_info":
				// SDK refresh semantics, not token verification alone or password context.
				f.collection.AuthRule = types.Pointer("@request.auth.id = id && @request.method = 'POST' && @request.context = 'default'")
				if f.app.Save(f.collection) != nil {
					t.Fatal("fixture request rule unavailable")
				}
				// SDK rotates the collection auth secret on AuthRule changes.
				// Mint a fixture-only token with the current key to isolate
				// CanAccessRecord behavior from signature invalidation.
				current, fetchErr := f.app.FindRecordById(f.collection.Id, f.record.Id)
				if fetchErr != nil {
					t.Fatal("current synthetic record unavailable")
				}
				token, fetchErr = current.NewAuthToken()
				if fetchErr != nil {
					t.Fatal("current synthetic token unavailable")
				}
				if _, e := f.adapter.Validate(context.Background(), token); e != nil {
					t.Fatal("SDK refresh RequestInfo mismatch")
				}
				f.collection.AuthRule = types.Pointer("@request.context = 'password'")
				if f.app.Save(f.collection) != nil {
					t.Fatal("fixture rule change unavailable")
				}
			}
			p, e := f.adapter.Validate(context.Background(), token)
			assertCode(t, e, "SITE_AUTH_INVALID")
			if strings.HasPrefix(change, "auth_rule") {
				current, fetchErr := f.app.FindRecordById(f.collection.Id, f.record.Id)
				if fetchErr != nil {
					t.Fatal("current synthetic record unavailable")
				}
				fresh, mintErr := current.NewAuthToken()
				if mintErr != nil {
					t.Fatal("current synthetic token unavailable")
				}
				_, ruleErr := f.adapter.Validate(context.Background(), fresh)
				if assertCode(t, ruleErr, "SITE_AUTH_INVALID").Kind() != failuretype.PolicyRefused {
					t.Fatal("current AuthRule not independently evaluated")
				}
			}
			if p != (Principal{}) {
				t.Fatal("invalidated credential retained principal")
			}
		})
	}
}
func TestSDKAuthRuleLoginAndOtherTokens(t *testing.T) {
	f := newFixture(t)
	for _, rule := range []*string{nil, types.Pointer("verified = true")} {
		f.collection.AuthRule = rule
		if f.app.Save(f.collection) != nil {
			t.Fatal("fixture rule unavailable")
		}
		token, _, e := f.adapter.Login(context.Background(), fixtureIdentity, fixturePassword)
		assertCode(t, e, "SITE_AUTH_REFUSED")
		if token != "" {
			t.Fatal("AuthRule bypass")
		}
	}
	f.collection.AuthRule = types.Pointer("")
	if f.app.Save(f.collection) != nil {
		t.Fatal("fixture rule unavailable")
	}
	reset, e := f.record.NewPasswordResetToken()
	if e != nil {
		t.Fatal("synthetic reset token unavailable")
	}
	_, e = f.adapter.Validate(context.Background(), reset)
	// PB's type-refusal error is untyped; do not invent a cause/category.
	assertCode(t, e, "SITE_AUTH_UNAVAILABLE")
	expired, e := f.record.NewStaticAuthToken(time.Nanosecond)
	if e != nil {
		t.Fatal("synthetic expired token unavailable")
	}
	_, e = f.adapter.Validate(context.Background(), expired)
	assertCode(t, e, "SITE_AUTH_INVALID")
	other := core.NewAuthCollection("synthetic_other")
	other.AuthAlert.Enabled = false
	if f.app.Save(other) != nil {
		t.Fatal("other collection unavailable")
	}
	record := core.NewRecord(other)
	record.SetEmail("other@example.invalid")
	record.SetPassword(fixturePassword)
	if f.app.Save(record) != nil {
		t.Fatal("other credential unavailable")
	}
	token, e := record.NewAuthToken()
	if e != nil {
		t.Fatal("other token unavailable")
	}
	_, e = f.adapter.Validate(context.Background(), token)
	assertCode(t, e, "SITE_AUTH_INVALID")
	_, e = New(f.app, "_superusers")
	assertCode(t, e, "SITE_AUTH_CONFIG")
}
func TestHostileSDKAndFinalResponse(t *testing.T) {
	for _, mode := range []string{"error", "panic", "after_final_error", "after_final_panic", "malformed", "overflow", "fake_success"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.app.OnRecordAuthRequest(f.collection.Name).BindFunc(func(e *core.RecordAuthRequestEvent) error {
				switch mode {
				case "error":
					return errors.New(hostile)
				case "panic":
					panic(hostile)
				case "after_final_error":
					if e.Next() != nil {
						return errors.New(hostile)
					}
					return errors.New(hostile)
				case "after_final_panic":
					_ = e.Next()
					panic(hostile)
				case "malformed":
					return e.String(200, hostile)
				case "overflow":
					return e.String(200, strings.Repeat("x", maxResponse+1))
				case "fake_success":
					return e.JSON(200, map[string]any{"token": hostile, "record": map[string]string{"id": hostile}})
				}
				return e.Next()
			})
			token, p, e := f.adapter.Login(context.Background(), fixtureIdentity, fixturePassword)
			if e == nil || token != "" || p != (Principal{}) {
				t.Fatal("unconfirmed final authentication accepted")
			}
			var d *ledger.Diagnostic
			if !errors.As(e, &d) {
				t.Fatal("untyped SDK failure")
			}
			assertCode(t, e, string(d.Code))
			assertQuiet(t, f)
		})
	}
}
func TestBoundsCancellationAndQuietConfiguration(t *testing.T) {
	f := newFixture(t)
	_, _, e := f.adapter.Login(context.Background(), strings.Repeat("x", 256), fixturePassword)
	assertCode(t, e, "SITE_AUTH_INPUT")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, e = f.adapter.Login(ctx, fixtureIdentity, fixturePassword)
	assertCode(t, e, "SITE_AUTH_INTERRUPTED")
	_, e = f.adapter.Validate(ctx, "synthetic")
	assertCode(t, e, "SITE_AUTH_INTERRUPTED")
	f.collection.AuthAlert.Enabled = true
	if f.app.Save(f.collection) != nil {
		t.Fatal("fixture alert setting unavailable")
	}
	_, _, e = f.adapter.Login(context.Background(), fixtureIdentity, fixturePassword)
	assertCode(t, e, "SITE_AUTH_CONFIG")
	assertQuiet(t, f)
}

// Actual local TLS transport surrounds the private SDK dispatch. The fixture
// returns a closed marker only: no session/routing implementation is added.
func TestActualTLSLoginFinalResponsePrivacy(t *testing.T) {
	f := newFixture(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("TLS absent")
			w.WriteHeader(500)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if r.ParseForm() != nil {
			w.WriteHeader(400)
			return
		}
		token, p, e := f.adapter.Login(r.Context(), r.PostForm.Get("identity"), r.PostForm.Get("password"))
		if e != nil {
			var d *ledger.Diagnostic
			if !errors.As(e, &d) {
				t.Error("untyped TLS error")
			}
			w.WriteHeader(401)
			_, _ = io.WriteString(w, "Sign-in refused.")
			return
		}
		if token == "" || p.RecordID != f.record.Id {
			t.Error("missing private principal")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(204)
	}))
	defer server.Close()
	for _, input := range []struct {
		identity, password string
		status             int
	}{{fixtureIdentity, fixturePassword, 204}, {fixtureIdentity, "incorrect-password", 401}, {"absent@example.invalid", "incorrect-password", 401}} {
		response, e := server.Client().PostForm(server.URL, url.Values{"identity": {input.identity}, "password": {input.password}})
		if e != nil {
			t.Fatal("local TLS transport unavailable")
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || response.StatusCode != input.status || strings.Contains(string(body), fixtureIdentity) || strings.Contains(string(body), fixturePassword) || strings.Contains(string(body), "token") || len(response.Cookies()) != 0 {
			t.Fatal("TLS final result or privacy mismatch")
		}
	}
	assertQuiet(t, f)
}
