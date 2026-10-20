package website

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

type hostileErrorMethods struct{}

func (hostileErrorMethods) Error() string { panic(hostileFailure) }
func (hostileErrorMethods) As(any) bool   { panic(hostileFailure) }

func TestAuthProjectionPreservesClosedTypesAndRejectsHostileFields(t *testing.T) {
	h := &Host{}
	for _, code := range []string{"SITE_AUTH_REFUSED", "SITE_AUTH_INVALID", "SITE_AUTH_CONFIG", "SITE_AUTH_INTERRUPTED", "SITE_AUTH_MFA_UNSUPPORTED", "SITE_AUTH_INPUT", "SITE_AUTH_UNAVAILABLE"} {
		source := httpFault(code)
		// Canonical typed numeric metadata survives, arbitrary text never does.
		source.Stage, source.Constraint, source.NextStep, source.Outcome = hostileFailure, hostileFailure, hostileFailure, hostileFailure
		d := h.authFailure(source, "SITE_AUTH_UNAVAILABLE")
		if d.Kind() != source.Kind() || strings.Contains(diagnosticText(d), hostileFailure) {
			t.Fatal("SITE_TEST_AUTH_TYPED_PROJECTION")
		}
	}
	for _, cause := range []error{hostileErrorMethods{}, context.Canceled, context.DeadlineExceeded} {
		d := h.authFailure(cause, "SITE_AUTH_UNAVAILABLE")
		if !d.Kind().Valid() || strings.Contains(diagnosticText(d), hostileFailure) {
			t.Fatal("SITE_TEST_HOSTILE_ERROR_METHOD_PRIVACY")
		}
	}
	// A known SDK rule-stage unknown must not become a fabricated credential
	// refusal, nor lose the precise closed component stage.
	source := httpFault("SITE_AUTH_UNAVAILABLE")
	source.Stage = "site_auth_rule"
	d := h.authFailure(source, "SITE_AUTH_UNAVAILABLE")
	if d.Stage != "site_auth_rule" || d.Code != "SITE_AUTH_UNAVAILABLE" || d.Kind() != failuretype.NativeAuthUnknown {
		t.Fatal("SITE_TEST_AUTH_STAGE_PRESERVED")
	}
}

func TestCommitFailureAndSubsequentErrorDeliveryHaveSeparateOwners(t *testing.T) {
	b := newBrowser(t)
	form := b.form(t, "/login")
	form.Set("key", testPassword)
	observer := &numericObserver{}
	b.host.observer = observer
	b.host.sessions.Store = faultyStore{Store: b.host.store, operation: "commit"}
	request := httptest.NewRequest("POST", "https://website.invalid/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://website.invalid")
	u, _ := url.Parse(b.server.URL)
	for _, cookie := range b.client.Jar.Cookies(u) {
		request.AddCookie(cookie)
	}
	writer := &failingResponse{header: make(http.Header)}
	captured := captureWebsiteOutput(t, func() { b.host.ServeHTTP(writer, request) })
	kinds := observer.counts()
	if len(kinds) != 2 || kinds[0] != failuretype.NativeWriteUnknown || kinds[1] != failuretype.ResponseDeliveryUnknown || writer.status != 503 || captured != "" {
		t.Fatal("SITE_TEST_COMMIT_DELIVERY_ONCE_ONLY")
	}
}

func TestSessionTypedFailuresRetainNumericCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, &ledger.Diagnostic{Code: ledger.CodeInput}} {
		b := newBrowser(t)
		form := b.form(t, "/login")
		form.Set("key", testPassword)
		observer := &numericObserver{}
		b.host.observer = observer
		b.host.sessions.Store = faultyStore{Store: b.host.store, operation: "commit", cause: cause}
		response, body := b.request(t, "POST", "/login", form, b.server.URL)
		assertClosedHTTP(t, response, body, "SITE_SESSION_UNAVAILABLE", "sdk_auth_completed_website_session_unconfirmed")
		kinds := observer.counts()
		if len(kinds) != 1 || kinds[0] != ledger.FailureKind(cause, failuretype.NativeWriteUnknown) {
			t.Fatal("SITE_TEST_SESSION_TYPED_CAUSE")
		}
	}
}
