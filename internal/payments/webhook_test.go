package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

func TestWebhookHTTPRejectsInvalidInputsBeforeProviderWork(t *testing.T) {
	f := newFixture(t, true)
	ref := f.start(t)
	cases := []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"wrong_path", func(r *http.Request) { r.URL.Path = "/other" }, 404},
		{"get", func(r *http.Request) { r.Method = "GET" }, 405},
		{"query", func(r *http.Request) { r.URL.RawQuery = "ref=" + privateSentinel }, 404},
		{"empty_query", func(r *http.Request) { r.URL.ForceQuery = true }, 404},
		{"escaped_path", func(r *http.Request) { r.URL.RawPath = "/payments/stripe/%77ebhook" }, 404},
		{"missing_signature", func(r *http.Request) { r.Header.Del("Stripe-Signature") }, 400},
		{"invalid_signature", func(r *http.Request) { r.Header.Set("Stripe-Signature", privateSentinel) }, 400},
		{"duplicate_signature", func(r *http.Request) { r.Header.Add("Stripe-Signature", privateSentinel) }, 400},
		{"large_declared", func(r *http.Request) { r.ContentLength = stripepay.MaxWebhookBytes + 1 }, 413},
		{"large_chunked", func(r *http.Request) {
			r.ContentLength = -1
			r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", stripepay.MaxWebhookBytes+1)))
		}, 413},
		{"missing_body", func(r *http.Request) { r.Body = nil }, 400},
		{"body_read_failure", func(r *http.Request) { r.Body = errorBody{} }, 400},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := signedRequest(t, event(ref))
			test.change(request)
			response := httptest.NewRecorder()
			f.service.WebhookHandler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("HTTP status %d, expected %d", response.Code, test.want)
			}
			if response.Header().Get("Location") != "" {
				t.Fatal("webhook redirected")
			}
			if strings.Contains(response.Body.String(), privateSentinel) || strings.Contains(response.Body.String(), ref) {
				t.Fatal("private failure response")
			}
			var reply webhookReply
			if json.Unmarshal(response.Body.Bytes(), &reply) != nil || reply.Error == nil || reply.Error.Code == "" || reply.Error.Stage == "" || reply.Error.Constraint == "" || reply.Error.Outcome == "" {
				t.Fatal("unclassified rejection")
			}
		})
	}
	for _, test := range []struct {
		name   string
		change func(object)
		want   int
	}{
		{"live", func(e object) { e["livemode"] = true }, 400},
		{"version", func(e object) { e["api_version"] = "2024-06-20" }, 400},
		{"context", func(e object) { e["context"] = privateSentinel }, 400},
		{"missing_reference", func(e object) { delete(e["data"].(object)["object"].(object), "client_reference_id") }, 400},
		{"invalid_reference", func(e object) { e["data"].(object)["object"].(object)["client_reference_id"] = privateSentinel }, 400},
		{"irrelevant", func(e object) { e["type"] = "customer.created" }, 200},
		{"refund_notification_inert", func(e object) { e["type"] = "charge.refunded" }, 200},
		{"dispute_notification_inert", func(e object) { e["type"] = "charge.dispute.created" }, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := event(ref)
			test.change(e)
			code, _ := f.deliver(t, e)
			if code != test.want {
				t.Fatal("signed event status")
			}
		})
	}
	if posts, gets := f.requests(); posts != 1 || gets != 0 {
		t.Fatal("invalid/irrelevant webhook initiated provider reads")
	}
	f.assertBalance(t, "0")
}

type errorBody struct{}

func (errorBody) Read([]byte) (int, error) { return 0, errors.New(privateSentinel) }
func (errorBody) Close() error             { return nil }

func TestProviderAndStorageFailuresRemainRetryableAndClosed(t *testing.T) {
	f := newFixture(t, true)
	ref := f.start(t)
	f.paid()
	f.change(func(f *fixture) { f.readFail = true })
	code, reply := f.deliver(t, event(ref))
	if code != 503 || reply.Error == nil || reply.Error.Code != string(stripepay.CodeProvider) || reply.Error.HTTPStatus != 503 {
		t.Fatal("provider failure lost typed evidence")
	}
	f.assertBalance(t, "0")
	f.change(func(f *fixture) { f.readFail = false })
	// A real storage failure remains 5xx. A closed ledger cannot authorize an
	// early success acknowledgement or trigger a replacement create request.
	if err := f.core.Close(); err != nil {
		t.Fatal(err)
	}
	code, reply = f.deliver(t, event(ref))
	if code != 503 || reply.Error == nil || reply.Error.Code != string(ledger.CodeClosed) {
		t.Fatal("storage failure acknowledged")
	}
	if posts, _ := f.requests(); posts != 1 {
		t.Fatal("failure triggered create replay")
	}
}

func TestPreparedPurchaseAndMismatchedKnownBindingDoNotTriggerReads(t *testing.T) {
	f := newFixture(t, true)
	p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: f.form})
	if err != nil {
		t.Fatal(err)
	}
	code, reply := f.deliver(t, event(p.Reference))
	if code != 200 || reply.Status != "ineligible" {
		t.Fatal("event claimed prepared purchase")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("unclaimed event initiated provider work")
	}
	ref := f.start(t)
	e := event(ref)
	e["data"].(object)["object"].(object)["id"] = "cs_test_other"
	code, reply = f.deliver(t, e)
	if code != 200 || reply.Status != "ineligible" {
		t.Fatal("known binding mismatch")
	}
	if posts, gets := f.requests(); posts != 1 || gets != 0 {
		t.Fatal("known binding mismatch initiated provider work")
	}
}

func TestZeroConfigurationAndTypedProjection(t *testing.T) {
	ctx := context.Background()
	var zero Service
	result, err := zero.StartTopup(ctx, testAccount, ledger.PaymentForm{}, "usd5")
	requireErrorType(t, err)
	if result.Reference != "" || result.URL != "" || zero.CheckoutOrigin() != "" || zero.CheckoutURLAllowed("http://127.0.0.1:9999/checkout") {
		t.Fatal("zero service enabled")
	}
	_, err = zero.TopupStatus(ctx, testAccount, strings.Repeat("a", 64))
	requireErrorType(t, err)
	if s, err := NewOffline(ctx, nil, nil); s != nil || err == nil {
		t.Fatal("empty constructor enabled")
	}
	response := httptest.NewRecorder()
	zero.WebhookHandler().ServeHTTP(response, signedRequest(t, event(strings.Repeat("a", 64))))
	if response.Code != 503 {
		t.Fatal("zero webhook enabled")
	}
	original := &ledger.Diagnostic{Code: ledger.CodeCommit, Stage: "transaction_completion", Constraint: "authoritative_commit_evidence", Outcome: "unknown", NextStep: "Inspect original identity."}
	projected := projectFailure(original)
	if projected.Code != string(original.Code) || projected.Outcome != "unknown" || !projected.CauseUnknown {
		t.Fatal("commit uncertainty erased")
	}
	bound := creationBindingFailure(original)
	var preserved *ledger.Diagnostic
	if !errors.As(bound, &preserved) || preserved != original || bound.Outcome != "checkout_created_local_binding_unconfirmed" {
		t.Fatal("binding lost primary and create outcome")
	}
	unknown := projectFailure(errors.New(privateSentinel))
	if unknown.Code != "PAYMENT_COMPONENT_CAUSE_UNKNOWN" || !unknown.CauseUnknown || unknown.Outcome != "unknown" {
		t.Fatal("unknown failure not classified")
	}
	// Not merely any Stripe error plus evidence can authorize sticky blocking.
	e := stripepay.Evidence{RefundObserved: true}
	for _, err := range []error{nil, errors.New(privateSentinel), &stripepay.Error{Code: stripepay.CodeEvidence, Stage: stripepay.StageCharge, Constraint: stripepay.ConstraintRefundUnavailable}, &stripepay.Error{Code: stripepay.CodeProvider, Stage: stripepay.StageCharge, Constraint: stripepay.ConstraintRefund, Outcome: stripepay.OutcomeReadUnavailable}} {
		if canonicalBlock(e, err) {
			t.Fatal("ordinary error authorized block")
		}
	}
}

type failedWriter struct {
	header     http.Header
	panicWrite bool
}

func (w *failedWriter) Header() http.Header { return w.header }
func (w *failedWriter) WriteHeader(int)     {}
func (w *failedWriter) Write([]byte) (int, error) {
	if w.panicWrite {
		panic(privateSentinel)
	}
	return 0, errors.New(privateSentinel)
}
func TestDeliveryFailurePreservesKnownCommitAndPanicDoesNotEscape(t *testing.T) {
	for _, panicWrite := range []bool{false, true} {
		err := writeWebhook(&failedWriter{header: http.Header{}, panicWrite: panicWrite}, 200, webhookReply{Status: "funded"})
		requireErrorType(t, err)
		var d *Error
		if !errors.As(err, &d) || d.Outcome != "funded" || d.Stage != "payment_webhook_delivery" {
			t.Fatal("delivery fault erased commit")
		}
	}
	f := newFixture(t, true)
	ref := f.start(t)
	f.paid()
	// Real funding followed by a failing response writer cannot be a rollback.
	f.service.WebhookHandler().ServeHTTP(&failedWriter{header: http.Header{}, panicWrite: true}, signedRequest(t, event(ref)))
	f.assertBalance(t, "5000000")
	if f.count(t, "payment_sales") != 1 {
		t.Fatal("known commit lost after presentation panic")
	}
	code, reply := f.deliver(t, event(ref))
	if code != 200 || reply.Status != "already_funded" {
		t.Fatal("delivery failure duplicated funding")
	}
}

func TestUnexpectedBodyPanicIsContentFreeAndOutcomeUnknown(t *testing.T) {
	f := newFixture(t, true)
	request := signedRequest(t, event(strings.Repeat("a", 64)))
	request.Body = panicBody{}
	response := httptest.NewRecorder()
	f.service.WebhookHandler().ServeHTTP(response, request)
	if response.Code != 503 || bytes.Contains(response.Body.Bytes(), []byte(privateSentinel)) {
		t.Fatal("panic escaped")
	}
	var reply webhookReply
	if json.Unmarshal(response.Body.Bytes(), &reply) != nil || reply.Error == nil || reply.Error.Outcome != "unknown" || !reply.Error.CauseUnknown {
		t.Fatal("panic invented known outcome")
	}
}

type panicBody struct{}

func (panicBody) Read([]byte) (int, error) { panic(privateSentinel) }
func (panicBody) Close() error             { return nil }
