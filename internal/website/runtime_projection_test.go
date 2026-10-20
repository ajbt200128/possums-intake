package website

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/pkg/lispsite"
)

func TestRenderHTTPProjectionPreservesOnlyClosedRuntimeEvidence(t *testing.T) {
	for _, source := range []*ledger.Diagnostic{
		fault("SITE_EXECUTION_LIMIT", "page_evaluation", "step_budget", "synthetic-private", failuretype.PolicyRefused),
		fault("SITE_EXECUTION_LIMIT", "page_evaluation", "evaluation_nesting", "synthetic-private", failuretype.PolicyRefused),
		fault("SITE_EXECUTION_INTERRUPTED", "page_evaluation", "context_cancelled", "synthetic-private", failuretype.Cancelled),
		fault("SITE_EXECUTION_INTERRUPTED", "page_evaluation", "context_deadline", "synthetic-private", failuretype.DeadlineExceeded),
		fault("SITE_PAGE_REFUSED", "page_evaluation", "declared_presentation_capability", "synthetic-private", failuretype.PolicyRefused),
		renderFault(), documentRenderFault(),
	} {
		source.NextStep = "synthetic-private"
		source.Outcome = "synthetic-private"
		got := renderHTTPFault(source)
		if got.Code != source.Code || got.Stage != source.Stage || got.Constraint != source.Constraint || got.Kind() != source.Kind() || strings.Contains(diagnosticText(got), "synthetic-private") {
			t.Fatal("SITE_TEST_RUNTIME_HTTP_PROJECTION")
		}
	}
	for _, source := range []error{errors.New("synthetic-private"), fault("SITE_EXECUTION_LIMIT", "synthetic-private", "synthetic-private", "synthetic-private", failuretype.PolicyRefused)} {
		got := renderHTTPFault(source)
		if got.Code != "SITE_RENDER_UNAVAILABLE" || strings.Contains(diagnosticText(got), "synthetic-private") {
			t.Fatal("SITE_TEST_RUNTIME_HTTP_HOSTILE_PROJECTION")
		}
	}
}

func TestLibraryProjectionWhitelistsEveryField(t *testing.T) {
	for _, source := range []lispsite.Diagnostic{
		{Code: "BUNDLE_REFUSED", Stage: "bundle_prepare", Constraint: "bounded_valid_revision", Category: lispsite.Refused},
		{Code: "BUNDLE_UNAVAILABLE", Stage: "bundle_prepare", Constraint: "preparation_cause_unknown", Category: lispsite.SetupUnknown},
		{Code: "RENDER_UNAVAILABLE", Stage: "page_evaluation", Constraint: "bounded_complete_page_cause_unknown", Category: lispsite.RenderUnknown},
		{Code: "PAGE_REFUSED", Stage: "page_evaluation", Constraint: "declared_presentation_capability", Category: lispsite.Refused},
		{Code: "EXECUTION_LIMIT", Stage: "bundle_prepare", Constraint: "step_budget", Category: lispsite.Refused},
		{Code: "EXECUTION_LIMIT", Stage: "bundle_prepare", Constraint: "evaluation_nesting", Category: lispsite.Refused},
		{Code: "EXECUTION_LIMIT", Stage: "page_evaluation", Constraint: "step_budget", Category: lispsite.Refused},
		{Code: "EXECUTION_LIMIT", Stage: "page_evaluation", Constraint: "evaluation_nesting", Category: lispsite.Refused},
		{Code: "EXECUTION_INTERRUPTED", Stage: "bundle_prepare", Constraint: "context_cancelled", Category: lispsite.Cancelled},
		{Code: "EXECUTION_INTERRUPTED", Stage: "bundle_prepare", Constraint: "context_deadline", Category: lispsite.DeadlineExceeded},
		{Code: "EXECUTION_INTERRUPTED", Stage: "page_evaluation", Constraint: "context_cancelled", Category: lispsite.Cancelled},
		{Code: "EXECUTION_INTERRUPTED", Stage: "page_evaluation", Constraint: "context_deadline", Category: lispsite.DeadlineExceeded},
	} {
		got, ok := coreDiagnostic(&source)
		wantKind := failuretype.PolicyRefused
		switch source.Category {
		case lispsite.SetupUnknown:
			wantKind = failuretype.RuntimeSetupUnknown
		case lispsite.RenderUnknown:
			wantKind = failuretype.ResponseEncodingUnknown
		case lispsite.Cancelled:
			wantKind = failuretype.Cancelled
		case lispsite.DeadlineExceeded:
			wantKind = failuretype.DeadlineExceeded
		}
		if !ok || string(got.Code) != "SITE_"+source.Code || got.Stage != source.Stage || got.Constraint != source.Constraint || got.Kind() != wantKind || got.Outcome != "no_effect" {
			t.Fatal("SITE_TEST_CORE_PROJECTION")
		}
		if source.Stage == "page_evaluation" {
			projected := renderHTTPFault(got)
			if projected.Code != got.Code || projected.Stage != got.Stage || projected.Constraint != got.Constraint || projected.Kind() != wantKind {
				t.Fatal("SITE_TEST_CORE_HTTP_PROJECTION")
			}
		}
		for _, mutate := range []func(*lispsite.Diagnostic){
			func(d *lispsite.Diagnostic) { d.Code = hostileFailure },
			func(d *lispsite.Diagnostic) { d.Stage = hostileFailure },
			func(d *lispsite.Diagnostic) { d.Constraint = hostileFailure },
			func(d *lispsite.Diagnostic) { d.Category = lispsite.Category(hostileFailure) },
			func(d *lispsite.Diagnostic) { d.Category = lispsite.Category("other") },
		} {
			bad := source
			mutate(&bad)
			if _, ok := coreDiagnostic(&bad); ok || strings.Contains(bad.Error(), hostileFailure) {
				t.Fatal("SITE_TEST_CORE_HOSTILE_TUPLE")
			}
		}
	}
	var nilDiagnostic *lispsite.Diagnostic
	for _, cause := range []error{nil, nilDiagnostic, errors.New(hostileFailure), &lispsite.Diagnostic{Code: "PAGE_REFUSED", Stage: "bundle_prepare", Constraint: "declared_presentation_capability", Category: lispsite.Refused}} {
		if _, ok := coreDiagnostic(cause); ok {
			t.Fatal("SITE_TEST_CORE_UNKNOWN_ADMITTED")
		}
	}
}

func TestAdapterPreservesActualPreparationDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Prepare(ctx, Fixture())
	if d := ledger.Failure(err); d.Code != "SITE_EXECUTION_INTERRUPTED" || d.Stage != "bundle_prepare" || d.Constraint != "context_cancelled" || d.Kind() != failuretype.Cancelled {
		t.Fatal("SITE_TEST_PREPARE_CANCEL_PROJECTION")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = Prepare(ctx, Fixture())
	if d := ledger.Failure(err); d.Code != "SITE_EXECUTION_INTERRUPTED" || d.Stage != "bundle_prepare" || d.Constraint != "context_deadline" || d.Kind() != failuretype.DeadlineExceeded {
		t.Fatal("SITE_TEST_PREPARE_DEADLINE_PROJECTION")
	}
	bundle := Fixture()
	bundle["init.el"] = `(dotimes (i 2147483647))`
	_, err = Prepare(context.Background(), bundle)
	if d := ledger.Failure(err); d.Code != "SITE_EXECUTION_LIMIT" || d.Stage != "bundle_prepare" || d.Constraint != "step_budget" || d.Kind() != failuretype.PolicyRefused {
		t.Fatal("SITE_TEST_PREPARE_LIMIT_PROJECTION")
	}
}

func TestActualHTTPPreservesInterruptionAndRenderDiagnostics(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "render"} {
		b := newBrowser(t)
		ctx, cancel := context.WithCancel(context.Background())
		code, constraint, kind := "SITE_EXECUTION_INTERRUPTED", "context_cancelled", failuretype.Cancelled
		if mode == "deadline" {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			constraint, kind = "context_deadline", failuretype.DeadlineExceeded
		}
		if mode == "render" {
			ctx = context.Background()
			for i := range b.host.revision.Routes {
				if b.host.revision.Routes[i].Name == "public-home" {
					b.host.revision.Routes[i].Entry = "missing"
				}
			}
			code, constraint, kind = "SITE_RENDER_UNAVAILABLE", "bounded_complete_page_cause_unknown", failuretype.ResponseEncodingUnknown
		}
		cancel()
		observer := &numericObserver{}
		b.host.observer = observer
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "https://example.test/", nil).WithContext(ctx)
		b.host.ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if recorder.Code != 503 || !strings.Contains(body, code) || !strings.Contains(body, "stage=page_evaluation") || !strings.Contains(body, "constraint="+constraint) || !strings.Contains(body, "outcome=no_action") {
			t.Fatal("SITE_TEST_HTTP_RUNTIME_EVIDENCE")
		}
		if kinds := observer.counts(); len(kinds) != 1 || kinds[0] != kind {
			t.Fatal("SITE_TEST_HTTP_RUNTIME_ONCE")
		}
	}
}

func TestActualHTTPPreservesKnownELPSExecutionLimit(t *testing.T) {
	b := newBrowser(t)
	bundle := Fixture()
	bundle["pages.el"] += `(defun spin (view preferences) (spin view preferences))`
	revision, err := Prepare(context.Background(), bundle)
	if err != nil {
		t.Fatal("SITE_TEST_LIMIT_PREPARE")
	}
	for i := range revision.Routes {
		if revision.Routes[i].Name == "public-home" {
			revision.Routes[i].Entry = "spin"
		}
	}
	b.host.revision = revision
	observer := &numericObserver{}
	b.host.observer = observer
	response, body := b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 503 || !strings.Contains(body, "SITE_EXECUTION_LIMIT") || !strings.Contains(body, "stage=page_evaluation") || !strings.Contains(body, "constraint=step_budget") || !strings.Contains(body, "outcome=no_action") {
		t.Fatal("SITE_TEST_HTTP_LIMIT_EVIDENCE")
	}
	kinds := observer.counts()
	if len(kinds) != 1 || kinds[0] != failuretype.PolicyRefused {
		t.Fatal("SITE_TEST_HTTP_LIMIT_ONCE")
	}
}
