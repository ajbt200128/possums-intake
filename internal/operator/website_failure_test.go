package operator

import (
	"net/http/httptest"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

func TestWebsitePublicationDiagnosticProjection(t *testing.T) {
	for _, stage := range []string{"publication_admission", "publication_commit", "publication_dispatch", "publication_swap", "publication_completion"} {
		d := &ledger.Diagnostic{Code: "SITE_PUBLICATION_UNAVAILABLE", Stage: stage, Constraint: "authoritative_code_selection_cause_unknown", Outcome: "website_selection_unconfirmed", NextStep: hostile}
		if stage == "publication_admission" {
			d.Outcome = "no_effect"
		}
		if stage == "publication_completion" {
			d.Outcome = "website_selection_committed_response_unconfirmed"
		}
		writer := httptest.NewRecorder()
		if !websitePublicationFailure(writer, 500, d) || strings.Contains(writer.Body.String(), hostile) || !strings.Contains(writer.Body.String(), stage) {
			t.Fatal("PUBLICATION_NATIVE_DIAGNOSTIC")
		}
	}
	d := &ledger.Diagnostic{Code: "SITE_PUBLICATION_REFUSED", Stage: "publication_prepare", Constraint: "bounded_renderable_code", Outcome: "no_effect", NextStep: hostile}
	writer := httptest.NewRecorder()
	if !websitePublicationFailure(writer, 500, d) || strings.Contains(writer.Body.String(), hostile) {
		t.Fatal("PUBLICATION_PREPARATION_DIAGNOSTIC")
	}
	d.Stage = hostile
	writer = httptest.NewRecorder()
	if websitePublicationFailure(writer, 500, d) || writer.Body.Len() != 0 {
		t.Fatal("PUBLICATION_HOSTILE_TUPLE_ACCEPTED")
	}
}
