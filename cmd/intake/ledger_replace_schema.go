package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/dbutils"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// Admission-only binding to accepted predecessor main 9a8b6ba. Never executed,
// migrated or exposed as a compatibility financial API. The independent historic
// SQL fixture pins this literal in tests; no runtime file or embedded SQL input.
const replaceSplitV2DDL = `
CREATE TABLE ledger_meta (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 schema_version INTEGER NOT NULL CHECK(schema_version=2),
 clock_high_water_ms INTEGER NOT NULL CHECK(typeof(clock_high_water_ms)='integer' AND clock_high_water_ms>=0 AND clock_high_water_ms<=9999999999999)
);
CREATE TABLE ledger_accounts (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=15 AND id NOT GLOB '*[^a-z0-9]*'),
 account_id TEXT NOT NULL CHECK(length(CAST(account_id AS BLOB)) BETWEEN 1 AND 128),
 balance TEXT NOT NULL CHECK(typeof(balance)='text' AND length(balance) BETWEEN 1 AND 20 AND balance NOT GLOB '*[^0-9]*' AND (balance='0' OR substr(balance,1,1) BETWEEN '1' AND '9') AND (length(balance)<20 OR balance<='18446744073709551615')),
 enabled INTEGER NOT NULL CHECK(typeof(enabled)='integer' AND enabled IN (0,1))
);
CREATE TABLE ledger_reservations (
 reservation_id TEXT PRIMARY KEY NOT NULL CHECK(length(reservation_id)=32 AND reservation_id NOT GLOB '*[^0-9a-f]*'),
 idempotency_key TEXT NOT NULL UNIQUE,
 account_id TEXT NOT NULL REFERENCES ledger_accounts(account_id) ON DELETE RESTRICT,
 maximum TEXT NOT NULL CHECK(typeof(maximum)='text' AND length(maximum) BETWEEN 1 AND 20 AND maximum NOT GLOB '*[^0-9]*' AND substr(maximum,1,1) BETWEEN '1' AND '9'),
 timeout_seconds INTEGER NOT NULL CHECK(typeof(timeout_seconds)='integer' AND timeout_seconds BETWEEN 1 AND 1800),
 initial_expires_at INTEGER NOT NULL CHECK(typeof(initial_expires_at)='integer' AND initial_expires_at>0 AND initial_expires_at<=9999999999999 AND initial_expires_at<=expires_at),
 expires_at INTEGER NOT NULL CHECK(typeof(expires_at)='integer' AND expires_at>0 AND expires_at<=9999999999999),
 state TEXT NOT NULL CHECK(state IN ('active','settled','refunded','expired')),
 charged_amount TEXT NULL CHECK(charged_amount IS NULL OR (typeof(charged_amount)='text' AND length(charged_amount) BETWEEN 1 AND 20 AND charged_amount NOT GLOB '*[^0-9]*' AND (charged_amount='0' OR substr(charged_amount,1,1) BETWEEN '1' AND '9'))),
 terminal_at INTEGER NULL CHECK(terminal_at IS NULL OR (typeof(terminal_at)='integer' AND terminal_at>0)),
 retain_until INTEGER NULL CHECK(retain_until IS NULL OR (typeof(retain_until)='integer' AND retain_until>terminal_at)),
 CHECK((state='active' AND charged_amount IS NULL AND terminal_at IS NULL AND retain_until IS NULL) OR (state!='active' AND charged_amount IS NOT NULL AND terminal_at IS NOT NULL AND retain_until IS NOT NULL)),
 CHECK(state NOT IN ('refunded','expired') OR charged_amount='0'),
 CHECK(state!='expired' OR terminal_at=expires_at),
 CHECK(state NOT IN ('settled','refunded') OR terminal_at<expires_at)
);
CREATE TABLE ledger_credits (
 idempotency_key TEXT PRIMARY KEY NOT NULL,
 account_id TEXT NOT NULL REFERENCES ledger_accounts(account_id) ON DELETE RESTRICT,
 amount TEXT NOT NULL CHECK(typeof(amount)='text' AND length(amount) BETWEEN 1 AND 20 AND amount NOT GLOB '*[^0-9]*' AND substr(amount,1,1) BETWEEN '1' AND '9'),
 balance_after TEXT NOT NULL CHECK(typeof(balance_after)='text' AND length(balance_after) BETWEEN 1 AND 20 AND balance_after NOT GLOB '*[^0-9]*' AND (balance_after='0' OR substr(balance_after,1,1) BETWEEN '1' AND '9')),
 committed_at INTEGER NOT NULL CHECK(typeof(committed_at)='integer' AND committed_at>0),
 retain_until INTEGER NOT NULL CHECK(typeof(retain_until)='integer' AND retain_until>committed_at)
);
CREATE UNIQUE INDEX ledger_accounts_account_id ON ledger_accounts (account_id);
CREATE INDEX ledger_reservations_account_active ON ledger_reservations(account_id,state,expires_at);
CREATE INDEX ledger_reservations_expiry ON ledger_reservations(state,expires_at);
CREATE INDEX ledger_reservations_prune ON ledger_reservations(retain_until) WHERE state!='active';
CREATE INDEX ledger_credits_prune ON ledger_credits(retain_until);
`

var errReplaceLayout = errors.New("replacement layout refused")

func inspectReplaceV2(ctx context.Context, dir string, observer failureowner.Observer) (result error) {
	p := &replaceProgress{stage: "replacement_admission"}
	owned := false
	defer func() {
		if recover() != nil {
			result = replaceFailure(nil, p, "LEDGER_REPLACE_LAYOUT_UNAVAILABLE", "read_only_schema_cause_unknown", failuretype.StorageValidationUnknown)
			owned = false
		}
		if result != nil && !owned {
			kind := failuretype.StorageValidationUnknown
			if errors.Is(result, errReplaceLayout) {
				kind = failuretype.PolicyRefused
			}
			if _, classified := result.(*ledger.Diagnostic); !classified {
				result = replaceFailure(result, p, "LEDGER_REPLACE_LAYOUT_REFUSED", "exact_split_v2_layout_and_metadata_required", kind)
			}
			failureowner.Observe(observer, ledger.FailureKind(result, kind))
		}
	}()
	work, cancel := context.WithTimeout(ctx, ledger.WorkBudget)
	defer cancel()
	for _, name := range []string{"auxiliary.db", "data.db"} {
		// openReplaceTree excludes all WAL/journal/SHM sidecars first. Immutable
		// is safe ONLY under that stopped-writer gate; it prevents even SQLite
		// shared-memory writes. No PocketBase bootstrap/settings save occurs.
		uri := (&url.URL{Scheme: "file", Path: filepath.Join(dir, name)}).String()
		db, err := dbx.Open("sqlite", uri+"?mode=ro&immutable=1&_pragma=query_only(ON)")
		if err != nil {
			return err
		}
		db.DB().SetMaxOpenConns(1)
		defer closeReplaceResource(db.Close, p, observer, &result, &owned)
		query := func(sql string, params dbx.Params) *dbx.Query {
			return db.NewQuery(sql).Bind(params).WithContext(work)
		}
		var count int
		if err := query("SELECT count(*) FROM sqlite_schema", nil).Row(&count); err != nil {
			return err
		}
		if count == 0 {
			return errReplaceLayout
		}
		// Auxiliary admission establishes only readable nonempty SQLite, not a
		// complete SDK inventory: all application contents inside this child
		// are explicitly disposable. Replay protection binds the financial/auth
		// predecessor below, never preservation of unrelated native tables.
		if name == "data.db" {
			return validateReplaceV2(query)
		}
	}
	return errReplaceLayout
}

func validateReplaceV2(query func(string, dbx.Params) *dbx.Query) error {
	var count int
	if err := query("SELECT count(*) FROM sqlite_schema WHERE (lower(name) GLOB 'ledger_*' OR lower(tbl_name) GLOB 'ledger_*' OR lower(name) IN ('accounts','customer_keys') OR lower(tbl_name) IN ('accounts','customer_keys')) AND sql IS NOT NULL", nil).Row(&count); err != nil {
		return err
	}
	if count != 13 { // nine fixed financial objects; customer table + three indexes
		return errReplaceLayout
	}
	for _, statement := range strings.Split(replaceSplitV2DDL, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		parts := strings.Fields(statement)
		name := parts[2]
		if parts[1] == "UNIQUE" {
			name = parts[3]
		}
		var matches int
		if err := query("SELECT count(*) FROM sqlite_schema WHERE name={:name} AND sql={:sql}", dbx.Params{"name": name, "sql": statement}).Row(&matches); err != nil {
			return err
		}
		if matches != 1 {
			return errReplaceLayout
		}
	}
	// Only singleton version metadata, never account/hold/credential values.
	if err := query("SELECT count(*) FROM ledger_meta WHERE singleton=1 AND schema_version=2", nil).Row(&count); err != nil {
		return err
	}
	if count != 1 {
		return errReplaceLayout
	}
	if err := query("SELECT count(*) FROM _collections WHERE lower(name) GLOB 'ledger_*' OR lower(name) IN ('accounts','customer_keys') OR id IN ('ledgeraccounts1','customerkeys001','ledgeraccounts2')", nil).Row(&count); err != nil {
		return err
	}
	if count != 2 {
		return errReplaceLayout
	}
	for _, want := range replacePredecessorCollections() {
		// Decode only bounded schema fields/indexes, not options/signing secrets
		// or record values. SDK FieldsList supplies its supported JSON model.
		var stored struct {
			Fields  core.FieldsList `db:"fields"`
			Indexes string          `db:"indexes"`
		}
		if err := query("SELECT fields,indexes FROM _collections WHERE id={:id} AND name={:name} AND type={:type} AND length(fields)<=32768 AND length(indexes)<=4096", dbx.Params{"id": want.Id, "name": want.Name, "type": want.Type}).One(&stored); err != nil {
			return err
		}
		fields, err := json.Marshal(stored.Fields)
		if err != nil {
			return err
		}
		expected, err := json.Marshal(want.Fields)
		if err != nil {
			return err
		}
		indexes, err := json.Marshal(want.Indexes)
		if err != nil {
			return err
		}
		if string(fields) != string(expected) || stored.Indexes != string(indexes) {
			return errReplaceLayout
		}
		if want.Name == "customer_keys" {
			// Supported field column declarations also bind the physical auth
			// table, not just its collection metadata. No auth rows are read.
			columns := make(map[string]string, len(want.Fields))
			for _, field := range want.Fields {
				columns[field.GetName()] = field.ColumnType(nil)
			}
			// Pure supported SQL builder: no DB connection and never Execute.
			expectedSQL := dbx.NewFromDB(nil, "sqlite").CreateTable("customer_keys", columns).SQL()
			if err := query("SELECT count(*) FROM sqlite_schema WHERE name='customer_keys' AND type='table' AND sql={:sql}", dbx.Params{"sql": expectedSQL}).Row(&count); err != nil {
				return err
			}
			if count != 1 {
				return errReplaceLayout
			}
			for _, index := range want.Indexes {
				if err := query("SELECT count(*) FROM sqlite_schema WHERE tbl_name='customer_keys' AND type='index' AND sql={:sql}", dbx.Params{"sql": dbutils.ParseIndex(index).Build()}).Row(&count); err != nil {
					return err
				}
				if count != 1 {
					return errReplaceLayout
				}
			}
		}
	}
	return nil
}

// These are transient expected schema models ONLY. They are never saved or
// returned to a financial/authentication service; there is no legacy runtime.
func replacePredecessorCollections() []*core.Collection {
	accounts := core.NewBaseCollection("ledger_accounts", "ledgeraccounts1")
	accounts.Fields.Add(&core.TextField{Name: "account_id", Required: true, Max: 128})
	accounts.Fields.Add(&core.TextField{Name: "balance", Required: true, Max: 20, Pattern: `^(0|[1-9][0-9]{0,19})$`})
	accounts.Fields.Add(&core.BoolField{Name: "enabled"})
	accounts.Indexes = append(accounts.Indexes, "CREATE UNIQUE INDEX ledger_accounts_account_id ON ledger_accounts (account_id)")
	customer := core.NewAuthCollection("customer_keys", "customerkeys001")
	customer.Fields.GetByName("email").(*core.EmailField).Required = false
	customer.Fields.Add(&core.TextField{Name: "key_lookup", Hidden: true, Required: true, Min: 67, Max: 67, Pattern: `^v1_[0-9a-f]{64}$`})
	customer.Fields.Add(&core.TextField{Name: "account_id", Required: true, Min: 1, Max: 128})
	customer.Indexes = append(customer.Indexes, "CREATE UNIQUE INDEX customer_key_lookup ON customer_keys (key_lookup)")
	return []*core.Collection{accounts, customer}
}
