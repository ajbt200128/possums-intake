package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestSchemaStartupProjectionClosedTemplates(t *testing.T) {
	for _, tc := range []struct {
		code                 ledger.Code
		constraint, observed string
	}{
		{"LEDGER_SCHEMA_OBJECT_MISSING", "required_schema_object_present", "A required schema object is absent."},
		{"LEDGER_SCHEMA_OBJECT_DEFINITION", "supported_schema_object_definition", "A required schema object has an incompatible definition."},
		{"LEDGER_SCHEMA_OBJECT_INVENTORY", "supported_schema_object_inventory", "The schema object inventory is incompatible; this check does not identify a particular missing object or historical version."},
		{"LEDGER_SCHEMA_ACCOUNT_MISSING", "required_account_collection_present", "The required account collection metadata is absent."},
		{"LEDGER_SCHEMA_ACCOUNT_METADATA", "compatible_account_collection_metadata", "The account collection metadata is incompatible."},
		{"LEDGER_SCHEMA_ACCOUNT_INVENTORY", "supported_account_collection_inventory", "The account collection inventory is incompatible."},
		{"LEDGER_SCHEMA_CONNECTION_SETTINGS", "required_connection_configuration", "A storage connection setting does not match the required configuration."},
		{"LEDGER_SCHEMA_LEDGER_METADATA", "valid_ledger_metadata", "The ledger metadata row or version invariant is not satisfied."},
		{"LEDGER_SCHEMA_PAYMENT_LAYOUT", "complete_compatible_payment_schema", "The optional payment object schema is incomplete or incompatible."},
		{"LEDGER_SCHEMA_PAYMENT_METADATA", "valid_payment_metadata", "The payment metadata invariant is not satisfied."},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			for _, phase := range []startupPhase{phaseInit, phaseOpen} {
				source := &ledger.Diagnostic{Code: ledger.CodeSchema, Constraint: tc.constraint, Stage: startupSentinel, Outcome: startupSentinel, NextStep: startupSentinel}
				r := startupRecord{1, "failed", phase, startupFailureCode(phase, source)}
				parent := t.TempDir()
				if r.Code != tc.code || writeStartupRecord(parent, r) != nil {
					t.Fatal("SCHEMA_STARTUP_RECORD_PROJECTION")
				}
				got, valid := readStartupRecord(parent)
				wire, err := json.Marshal(got)
				var fields map[string]any
				if !valid || got != r || err != nil || len(wire) > startupMaxBytes || json.Unmarshal(wire, &fields) != nil || len(fields) != 4 {
					t.Fatal("SCHEMA_STARTUP_RECORD_SHAPE")
				}
				view := startupHealthDiagnostic(got)
				next := tc.observed + " Underlying cause unknown. Preserve state and inspect the schema privately; no reset, automatic migration or financial restore is authorized."
				want := healthDiagnostic(tc.code, string(phase), tc.constraint, next)
				if !reflect.DeepEqual(view, want) || view.Outcome != "not_ready" {
					t.Fatal("SCHEMA_STARTUP_EXACT_HEALTH_TEMPLATE")
				}
				wire, err = json.Marshal(view)
				if err != nil || bytes.Contains(wire, []byte(startupSentinel)) {
					t.Fatal("SCHEMA_STARTUP_SOURCE_FIELDS_LEAKED")
				}
				if startupFailureCode(phase, &ledger.Diagnostic{Code: tc.code}) != startupUnknown(phase) {
					t.Fatal("SCHEMA_STARTUP_PROJECTED_CODE_ACCEPTED_AS_SOURCE")
				}
			}
			for _, phase := range []startupPhase{phaseValidation, phaseVolumePrepare, phaseScratch, phaseHandler, phaseWebsite, phaseBind} {
				if startupCodeAllowed(phase, tc.code) || validStartupRecord(startupRecord{1, "failed", phase, tc.code}) || startupFailureCode(phase, &ledger.Diagnostic{Code: ledger.CodeSchema, Constraint: tc.constraint}) != startupUnknown(phase) {
					t.Fatal("SCHEMA_STARTUP_WRONG_PHASE_ACCEPTED")
				}
			}
			if (&ledger.Diagnostic{Code: tc.code}).Kind().Valid() {
				t.Fatal("SCHEMA_STARTUP_CODE_BECAME_ACCOUNTING_CATEGORY")
			}
		})
	}
	for _, constraint := range []string{"", startupSentinel, "supported_schema_and_metadata", "supported_schema_object_inventory suffix"} {
		source := &ledger.Diagnostic{Code: ledger.CodeSchema, Constraint: constraint, NextStep: startupSentinel}
		code := startupFailureCode(phaseOpen, source)
		view := startupHealthDiagnostic(startupRecord{1, "failed", phaseOpen, code})
		want := healthDiagnostic(ledger.CodeSchema, "open", "completed_startup_checkpoint", "Underlying startup cause unknown. Inspect runtime configuration and temporary storage privately; do not reuse or reinitialize partial state.")
		if code != ledger.CodeSchema || !reflect.DeepEqual(view, want) {
			t.Fatal("SCHEMA_STARTUP_GENERIC_FALLBACK_CHANGED")
		}
	}
}

// Mutations use the actual SDK and SQLite, then ordinary OpenObserved. No test
// supplies a fabricated schema Diagnostic to the startup projection here.
func TestSchemaStartupActualOpenObservedFixtures(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		code       ledger.Code
		constraint string
		mutate     func(core.App)
	}{
		{"missing_index_inventory", "LEDGER_SCHEMA_OBJECT_INVENTORY", "supported_schema_object_inventory", func(app core.App) { schemaFixtureSQL(t, app, "DROP INDEX ledger_reservations_expiry") }},
		{"missing_required_object", "LEDGER_SCHEMA_OBJECT_MISSING", "required_schema_object_present", func(app core.App) {
			schemaFixtureSQL(t, app, "DROP INDEX ledger_reservations_expiry; CREATE INDEX ledger_extra ON ledger_reservations(expires_at)")
		}},
		{"object_definition", "LEDGER_SCHEMA_OBJECT_DEFINITION", "supported_schema_object_definition", func(app core.App) {
			schemaFixtureSQL(t, app, "DROP INDEX ledger_reservations_expiry; CREATE INDEX ledger_reservations_expiry ON ledger_reservations(expires_at)")
		}},
		{"account_missing", "LEDGER_SCHEMA_ACCOUNT_MISSING", "required_account_collection_present", func(app core.App) { schemaFixtureSQL(t, app, "DELETE FROM _collections WHERE id='ledgeraccounts2'") }},
		{"account_list_rule", "LEDGER_SCHEMA_ACCOUNT_METADATA", "compatible_account_collection_metadata", func(app core.App) {
			c, err := app.FindCollectionByNameOrId(ledger.AccountCollectionID)
			if err != nil {
				t.Fatal("SCHEMA_SDK_COLLECTION_FIXTURE")
			}
			rule := ""
			c.ListRule = &rule
			if app.Save(c) != nil {
				t.Fatal("SCHEMA_SDK_RULE_FIXTURE")
			}
		}},
		{"account_inventory", "LEDGER_SCHEMA_ACCOUNT_INVENTORY", "supported_account_collection_inventory", func(app core.App) {
			if app.Save(core.NewBaseCollection("ledger_extra")) != nil {
				t.Fatal("SCHEMA_SDK_INVENTORY_FIXTURE")
			}
			schemaFixtureSQL(t, app, "DROP TABLE ledger_extra")
		}},
		{"incomplete_payment", "LEDGER_SCHEMA_PAYMENT_LAYOUT", "complete_compatible_payment_schema", func(app core.App) { schemaFixtureSQL(t, app, "CREATE TABLE payment_partial(value TEXT)") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			if ledger.Init(ctx, dir) != nil {
				t.Fatal("SCHEMA_FRESH_INIT_FIXTURE")
			}
			app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
			if app.Bootstrap() != nil {
				t.Fatal("SCHEMA_SDK_BOOTSTRAP_FIXTURE")
			}
			tc.mutate(app)
			if app.ClearBootstrap() != nil {
				t.Fatal("SCHEMA_SDK_CLOSE_FIXTURE")
			}
			before := schemaFixtureSnapshot(t, dir)
			schemaFixtureOpenHealth(t, dir, tc.code, tc.constraint)
			if !reflect.DeepEqual(before, schemaFixtureSnapshot(t, dir)) {
				t.Fatal("SCHEMA_REFUSAL_MODIFIED_DATABASE")
			}
		})
	}
	t.Run("split_v2", func(t *testing.T) {
		dir := replaceV2Fixture(t, nil)
		before := schemaFixtureSnapshot(t, dir)
		schemaFixtureOpenHealth(t, dir, "LEDGER_SCHEMA_OBJECT_INVENTORY", "supported_schema_object_inventory")
		if !reflect.DeepEqual(before, schemaFixtureSnapshot(t, dir)) {
			t.Fatal("SCHEMA_HISTORICAL_REFUSAL_MODIFIED_DATABASE")
		}
	})
	t.Run("sdk_only_partial", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "state")
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
		if app.Bootstrap() != nil || app.ClearBootstrap() != nil {
			t.Fatal("SCHEMA_SDK_PARTIAL_FIXTURE")
		}
		schemaFixtureOpenHealth(t, dir, "LEDGER_SCHEMA_OBJECT_INVENTORY", "supported_schema_object_inventory")
	})
	t.Run("ledger_metadata", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "state")
		if ledger.Init(ctx, dir) != nil {
			t.Fatal("SCHEMA_FRESH_INIT_FIXTURE")
		}
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
		if app.Bootstrap() != nil {
			t.Fatal("SCHEMA_METADATA_FIXTURE")
		}
		schemaFixtureSQL(t, app, "DELETE FROM ledger_meta")
		if app.ClearBootstrap() != nil {
			t.Fatal("SCHEMA_METADATA_CLOSE_FIXTURE")
		}
		schemaFixtureOpenHealth(t, dir, "LEDGER_SCHEMA_LEDGER_METADATA", "valid_ledger_metadata")
	})
	t.Run("payment_metadata", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "state")
		if ledger.Init(ctx, dir) != nil {
			t.Fatal("SCHEMA_FRESH_INIT_FIXTURE")
		}
		s, err := ledger.Open(ctx, dir)
		if err != nil || s.InitializeSyntheticPayments(ctx) != nil || s.Close() != nil {
			t.Fatal("SCHEMA_PAYMENT_SETUP_FIXTURE")
		}
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
		if app.Bootstrap() != nil {
			t.Fatal("SCHEMA_PAYMENT_METADATA_FIXTURE")
		}
		schemaFixtureSQL(t, app, "DELETE FROM payment_meta")
		if app.ClearBootstrap() != nil {
			t.Fatal("SCHEMA_PAYMENT_CLOSE_FIXTURE")
		}
		schemaFixtureOpenHealth(t, dir, "LEDGER_SCHEMA_PAYMENT_METADATA", "valid_payment_metadata")
	})
	t.Run("fresh_positive", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "state")
		if ledger.Init(ctx, dir) != nil {
			t.Fatal("SCHEMA_FRESH_INIT_FIXTURE")
		}
		o := &runtimeObserver{}
		s, err := ledger.OpenObserved(ctx, dir, o)
		if err != nil || s == nil || !s.Ready() || s.Close() != nil {
			t.Fatal("SCHEMA_FRESH_POSITIVE_FAILED")
		}
		o.want(t)
	})
}

func schemaFixtureSQL(t *testing.T, app core.App, statement string) {
	t.Helper()
	if _, err := app.DB().NewQuery(statement).Execute(); err != nil {
		t.Fatal("SCHEMA_SQL_FIXTURE_UNAVAILABLE")
	}
}

func schemaFixtureSnapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	for _, name := range []string{"data.db", "auxiliary.db"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal("SCHEMA_SNAPSHOT_FIXTURE_UNAVAILABLE")
		}
		out[name] = sha256.Sum256(raw)
	}
	return out
}

func schemaFixtureOpenHealth(t *testing.T, dir string, code ledger.Code, constraint string) {
	t.Helper()
	o := &runtimeObserver{}
	s, err := ledger.OpenObserved(context.Background(), dir, o)
	if s != nil {
		s.Close()
		t.Fatal("SCHEMA_INCOMPATIBLE_STATE_ADMITTED")
	}
	d, ok := err.(*ledger.Diagnostic)
	if !ok || d == nil || d.Code != ledger.CodeSchema || d.Constraint != constraint || d.Kind() != failuretype.SchemaRejected {
		t.Fatal("SCHEMA_OPEN_EXACT_REFINEMENT")
	}
	o.want(t, failuretype.SchemaRejected)
	r := startupRecord{1, "failed", phaseOpen, startupFailureCode(phaseOpen, d)}
	parent := t.TempDir()
	if writeStartupRecord(parent, r) != nil {
		t.Fatal("SCHEMA_ACTUAL_OPEN_RECORD")
	}
	got, valid := readStartupRecord(parent)
	view := startupHealthDiagnostic(got)
	if !valid || view.Code != code || view.Constraint != constraint || view.Stage != "open" || view.Outcome != "not_ready" || view.NextStep != d.NextStep {
		t.Fatal("SCHEMA_ACTUAL_OPEN_HEALTH_DETAIL")
	}
}
