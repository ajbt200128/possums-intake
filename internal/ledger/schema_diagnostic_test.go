package ledger

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"possums-intake/internal/failuretype"
)

func TestSchemaReasonsCanonicalClosedAndFatal(t *testing.T) {
	for _, reason := range []schemaReason{
		schemaObjectMissing, schemaObjectDefinition, schemaObjectInventory,
		schemaAccountMissing, schemaAccountMetadata, schemaAccountInventory,
		schemaConnectionSettings, schemaLedgerMetadata, schemaPaymentLayout, schemaPaymentMetadata,
	} {
		t.Run(string(reason), func(t *testing.T) {
			want := schemaFailure(reason)
			if want.Code != CodeSchema || want.Constraint != string(reason) || want.Kind() != failuretype.SchemaRejected || !strings.Contains(want.NextStep, "Underlying cause unknown.") {
				t.Fatal("schema reason lost its closed classification")
			}
			hostile := &Diagnostic{Code: CodeSchema, Constraint: string(reason), Stage: "SYNTHETIC_PRIVATE_STAGE", Outcome: "SYNTHETIC_PRIVATE_OUTCOME", NextStep: "SYNTHETIC_PRIVATE_SECRET"}
			got := CanonicalFailure(hostile, failuretype.StorageValidationUnknown)
			if *got != *want {
				t.Fatal("canonical schema template differs or copied hostile fields")
			}
			s, _, _ := testService(t)
			ctx, report := WithFailureReport(background)
			err := s.run(ctx, func(*transaction) error { return hostile }, false)
			d := validationDiagnostic(t, err, CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
			if *d != *want || s.Ready() {
				t.Fatal("schema refinement changed rollback classification or fatal readiness")
			}
			kinds, count, valid := report.Take()
			if !valid || count != 1 || kinds[0] != failuretype.SchemaRejected {
				t.Fatal("schema refinement changed once-only failure projection")
			}
		})
	}
	for _, reason := range []schemaReason{"", "SYNTHETIC_PRIVATE_SECRET", "supported_schema_object_inventory suffix"} {
		d := CanonicalFailure(&Diagnostic{Code: CodeSchema, Constraint: string(reason), NextStep: "SYNTHETIC_PRIVATE_SECRET"}, failuretype.StorageValidationUnknown)
		if *d != *diagnostic(CodeSchema) {
			t.Fatal("unknown schema reason did not retain exact generic template")
		}
		wire, err := json.Marshal(d)
		if err != nil || strings.Contains(string(wire), "SYNTHETIC_PRIVATE") {
			t.Fatal("unknown schema reason leaked")
		}
	}
}

func TestSchemaValidationObservedRefinements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sql    string
		reason schemaReason
	}{
		{"object_inventory", "DROP INDEX ledger_reservations_expiry", schemaObjectInventory},
		{"object_missing", "DROP INDEX ledger_reservations_expiry; CREATE INDEX ledger_unexpected ON ledger_reservations(expires_at)", schemaObjectMissing},
		{"object_definition", "DROP INDEX ledger_reservations_expiry; CREATE INDEX ledger_reservations_expiry ON ledger_reservations(expires_at)", schemaObjectDefinition},
		{"account_missing", "DELETE FROM _collections WHERE id='ledgeraccounts2'", schemaAccountMissing},
		{"account_metadata", "UPDATE _collections SET fields='[]' WHERE id='ledgeraccounts2'", schemaAccountMetadata},
		{"ledger_metadata", "DELETE FROM ledger_meta", schemaLedgerMetadata},
		{"payment_layout", "CREATE TABLE payment_partial(value TEXT)", schemaPaymentLayout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := testService(t)
			if _, err := s.app.DB().NewQuery(tc.sql).Execute(); err != nil {
				t.Fatal("synthetic schema mutation unavailable")
			}
			d := validationDiagnostic(t, s.validate(background), CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
			if *d != *schemaFailure(tc.reason) || s.Ready() {
				t.Fatal("observed schema condition lost its exact template or fatal readiness")
			}
		})
	}
	t.Run("connection_settings", func(t *testing.T) {
		s, _, _ := testService(t)
		base := s.app
		defer func() { s.app = base }()
		s.app = &validationReadApp{App: base, queryHook: func(statement string, q *dbx.Query) *dbx.Query {
			if statement == "PRAGMA synchronous" {
				return base.DB().NewQuery("SELECT 0")
			}
			return q
		}}
		d := validationDiagnostic(t, s.validate(background), CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
		if *d != *schemaFailure(schemaConnectionSettings) || s.Ready() {
			t.Fatal("observed connection mismatch lost its exact template")
		}
	})
	t.Run("payment_metadata", func(t *testing.T) {
		s, _, _ := testService(t)
		if s.InitializeSyntheticPayments(background) != nil {
			t.Fatal("synthetic payment initialization unavailable")
		}
		if _, err := s.app.DB().NewQuery("DELETE FROM payment_meta").Execute(); err != nil {
			t.Fatal("synthetic payment metadata mutation unavailable")
		}
		d := validationDiagnostic(t, s.validate(background), CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
		if *d != *schemaFailure(schemaPaymentMetadata) || s.Ready() {
			t.Fatal("observed payment metadata mismatch lost its exact template")
		}
	})
}
