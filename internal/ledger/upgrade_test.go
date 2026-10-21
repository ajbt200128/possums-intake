package ledger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
)

// This fixture deliberately creates the literal legacy schema, never current
// initialization or an automatic downgrade of actual state.
func legacyService(t *testing.T) (*Service, string, int64) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal("legacy directory unavailable")
	}
	app, err := bootstrap(dir, true)
	must(t, app, err)
	s := newService(app)
	t.Cleanup(func() { s.Close() })
	now := time.Now().Add(time.Hour).UnixMilli()
	prepared, err := prepareOperator(background, app, OperatorCredentials{Email: "synthetic@example.invalid", Password: "synthetic-upgrade-password"})
	must(t, prepared, err)
	err = s.run(background, func(tx *transaction) error {
		if err := tx.app.SaveWithContext(tx.ctx, accountCollection()); err != nil {
			return err
		}
		if err := tx.exec("DROP TABLE ledger_accounts", nil); err != nil {
			return err
		}
		if err := tx.exec(schemaV1, nil); err != nil {
			return err
		}
		if err := initializeOperator(tx, prepared); err != nil {
			return err
		}
		if _, err := tx.createAccount(NativeAccountCreate{AccountID: "a", Balance: "93", Enabled: true}); err != nil {
			return err
		}
		if _, err := tx.createAccount(NativeAccountCreate{AccountID: "disabled", Balance: "0", Enabled: false}); err != nil {
			return err
		}
		if err := tx.exec("UPDATE ledger_meta SET clock_high_water_ms={:now}", dbx.Params{"now": now}); err != nil {
			return err
		}
		if err := tx.exec("INSERT INTO ledger_credits("+creditColumns+") VALUES({:key},'a','100','100',{:at},{:retain})", dbx.Params{"key": key(now, 1), "at": now - 1000, "retain": now + KeyWindowMS}); err != nil {
			return err
		}
		legacyColumns := strings.Replace(reservationColumns, "initial_expires_at,", "", 1)
		for i, state := range []State{Active, Settled, Refunded, Expired} {
			expires := now + 600000
			timeout := 600
			var charge, terminal, retain any
			if state != Active {
				charge, terminal, retain = "0", now, now+KeyWindowMS
				if state == Settled {
					charge = "7"
				}
				if state == Expired {
					expires, timeout, terminal = now-1000, 1, now-1000
				}
			}
			if err := tx.exec("INSERT INTO ledger_reservations("+legacyColumns+") VALUES({:id},{:key},'a','30',{:timeout},{:expires},{:state},{:charge},{:terminal},{:retain})", dbx.Params{"id": fmt.Sprintf("%032x", i+2), "key": key(now, i+2), "timeout": timeout, "expires": expires, "state": string(state), "charge": charge, "terminal": terminal, "retain": retain}); err != nil {
				return err
			}
		}
		return validateState(tx, 1, false)
	}, false)
	if err != nil {
		t.Fatalf("legacy fixture invalid: %s", Failure(err).Code)
	}
	return s, dir, now
}

type upgradeSnapshot struct {
	Accounts []struct {
		ID string `db:"id"`
		Account
	}
	Reservations []Reservation
	Credits      []Credit
	High         int64
	Credentials  string
}

func snapshotUpgrade(t *testing.T, s *Service, version int) upgradeSnapshot {
	t.Helper()
	var out upgradeSnapshot
	err := s.run(background, func(tx *transaction) error {
		tx.legacy = version == 1
		if err := tx.query("SELECT id,account_id,balance,enabled FROM ledger_accounts ORDER BY account_id", nil).All(&out.Accounts); err != nil {
			return err
		}
		if err := tx.query("SELECT "+tx.reservationColumns()+" FROM ledger_reservations ORDER BY reservation_id", nil).All(&out.Reservations); err != nil {
			return err
		}
		if err := tx.query("SELECT "+creditColumns+" FROM ledger_credits ORDER BY idempotency_key", nil).All(&out.Credits); err != nil {
			return err
		}
		if err := tx.query("SELECT clock_high_water_ms FROM ledger_meta", nil).Row(&out.High); err != nil {
			return err
		}
		// Only synthetic credentials, compared in memory and never printed.
		return tx.query("SELECT coalesce(group_concat(id || tokenKey || password,','),'') FROM _superusers", nil).Row(&out.Credentials)
	}, false)
	if err != nil {
		t.Fatal("private upgrade comparison unavailable")
	}
	return out
}

func TestUpgradePopulatedV1PreservesStateAndExactFreshDDL(t *testing.T) {
	s, dir, now := legacyService(t)
	before := snapshotUpgrade(t, s, 1)
	if len(before.Accounts) != 2 || len(before.Reservations) != 4 || len(before.Credits) != 1 || before.Credentials == "" {
		t.Fatal("populated fixture incomplete")
	}
	if err := s.Close(); err != nil {
		t.Fatal("legacy close failed")
	}
	// Serve must refuse v1 without changing its layout or accounting.
	_, err := Open(background, dir)
	codeIs(t, err, CodeSchema)
	o := &lifecycleObserver{}
	if err := UpgradeObserved(background, dir, o); err != nil {
		t.Fatalf("upgrade refused: %s", Failure(err).Code)
	}
	o.want(t)
	app, err := bootstrap(dir, false)
	must(t, app, err)
	upgraded := newService(app)
	if after := snapshotUpgrade(t, upgraded, 2); !reflect.DeepEqual(after, before) {
		t.Fatal("upgrade altered legacy fields, watermark or credentials")
	}
	if err := upgraded.run(background, func(tx *transaction) error { return validateState(tx, 2, false) }, false); err != nil {
		t.Fatal("migrated DDL differs from fresh v2")
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal("upgrade inspection close failed")
	}
	if err := Upgrade(background, dir); err != nil {
		t.Fatal("validated v2 rerun refused")
	}
	app, err = bootstrap(dir, false)
	must(t, app, err)
	upgraded = newService(app)
	if after := snapshotUpgrade(t, upgraded, 2); !reflect.DeepEqual(after, before) {
		t.Fatal("v2 rerun mutated state")
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal("rerun inspection close failed")
	}
	opened, err := Open(background, dir)
	must(t, opened, err)
	defer opened.Close()
	row, err := opened.Lookup(background, fmt.Sprintf("%032x", 2))
	must(t, row, err)
	advance(t, opened, now+1000)
	out := renew(t, opened, row)
	if out.Reservation.InitialExpiresAt != row.ExpiresAt || out.Reservation.ExpiresAt != row.ExpiresAt+1000 {
		t.Fatal("migrated hold cannot renew")
	}
	if b := balance(t, opened, "a"); b.AccountBalance.Balance != "93" || b.Available != "63" {
		t.Fatal("upgrade or renewal changed money")
	}
}

func TestUpgradeRejectsMissingUnknownAndCorruptLegacyWithoutRepair(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	codeIs(t, Upgrade(background, missing), CodeMissing)
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("upgrade created absent state")
	}
	codeIs(t, Upgrade(background, "relative"), CodePath)
	for _, fixture := range []struct {
		name, mutation string
		code           Code
	}{
		{"missing_index", "DROP INDEX ledger_reservations_expiry", CodeSchema},
		{"extra_object", "CREATE TABLE ledger_unrecognized (value TEXT)", CodeSchema},
		{"missing_meta", "DELETE FROM ledger_meta", CodeSchema},
		{"impossible_hold", "UPDATE ledger_accounts SET balance='29' WHERE account_id='a'", CodeCorrupt},
		{"invalid_key", "UPDATE ledger_reservations SET idempotency_key='invalid' WHERE state='active'", CodeCorrupt},
		{"invalid_credit", "UPDATE ledger_credits SET amount='18446744073709551616'", CodeCorrupt},
		{"terminal_evidence", "UPDATE ledger_reservations SET charged_amount='18446744073709551616' WHERE state='settled'", CodeCorrupt},
		{"original_window", "UPDATE ledger_reservations SET expires_at=1 WHERE state='active'", CodeCorrupt},
		{"native_metadata", "DELETE FROM _collections WHERE id='ledgeraccounts1'", CodeSchema},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s, dir, _ := legacyService(t)
			if err := s.run(background, func(tx *transaction) error { return tx.exec(fixture.mutation, nil) }, false); err != nil {
				t.Fatal("corrupt fixture unavailable")
			}
			var before Money
			if err := s.run(background, func(tx *transaction) error {
				return tx.query("SELECT balance FROM ledger_accounts WHERE account_id='a'", nil).Row(&before)
			}, false); err != nil {
				t.Fatal("private comparison unavailable")
			}
			if err := s.Close(); err != nil {
				t.Fatal("fixture close failed")
			}
			err := Upgrade(background, dir)
			codeIs(t, err, fixture.code)
			db, err := connect(filepath.Join(dir, "data.db"), false)
			must(t, db, err)
			defer db.Close()
			var initialColumns int
			if err := db.NewQuery("SELECT count(*) FROM pragma_table_info('ledger_reservations') WHERE name='initial_expires_at'").Row(&initialColumns); err != nil || initialColumns != 0 {
				t.Fatal("refused upgrade changed original table")
			}
			if fixture.name != "missing_meta" {
				var version int
				if err := db.NewQuery("SELECT schema_version FROM ledger_meta").Row(&version); err != nil || version != 1 {
					t.Fatal("refused upgrade changed metadata")
				}
			}
			// Compare money/rows directly, even if framework metadata is broken.
			var total string
			if err := db.NewQuery("SELECT balance FROM ledger_accounts WHERE account_id='a'").Row(&total); err != nil || Money(total) != before {
				t.Fatal("refused upgrade repaired financial state")
			}
		})
	}
}

func TestUpgradeDoesNotRewriteNativeDependentSchema(t *testing.T) {
	for _, statement := range []string{
		"CREATE TABLE synthetic_native (reservation TEXT REFERENCES ledger_reservations(reservation_id) ON DELETE CASCADE)",
		"CREATE VIEW synthetic_native AS SELECT reservation_id FROM ledger_reservations",
	} {
		s, _, _ := legacyService(t)
		if err := s.run(background, func(tx *transaction) error { return tx.exec(statement, nil) }, false); err != nil {
			t.Fatal("dependent fixture unavailable")
		}
		err := s.run(background, upgradeTransaction, false)
		if err == nil {
			t.Fatal("upgrade committed rewritten native schema")
		}
		var stored string
		// Schema refusals intentionally close this private Service. Inspect the
		// actual rolled-back database through a fresh test-only service gate.
		if err := newService(s.app).run(background, func(tx *transaction) error {
			if err := validateState(tx, 1, false); err != nil {
				return err
			}
			return tx.query("SELECT sql FROM sqlite_schema WHERE name='synthetic_native'", nil).Row(&stored)
		}, false); err != nil || stored != statement {
			t.Fatal("refused upgrade changed native schema or legacy layout")
		}
	}
}

func TestUpgradeTransactionRollbackAndLostCompletion(t *testing.T) {
	for _, mode := range []string{"during_ddl", "after_validation", "lost_completion"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := legacyService(t)
			before := snapshotUpgrade(t, s, 1)
			base := s.app
			if mode == "during_ddl" {
				s.app = &validationReadApp{App: base, queryHook: func(statement string, q *dbx.Query) *dbx.Query {
					if strings.HasPrefix(statement, "CREATE INDEX ledger_reservations_expiry") {
						q.LastError = errors.New("SYNTHETIC_HOSTILE_UPGRADE_FAULT")
					}
					return q
				}}
			} else if mode == "lost_completion" {
				s.app = lostCompletionApp{base}
			}
			ctx, report := WithFailureReport(background)
			err := s.run(ctx, func(tx *transaction) error {
				if err := upgradeTransaction(tx); err != nil {
					return err
				}
				if mode == "after_validation" {
					return diagnostic(CodeInput)
				}
				return nil
			}, false)
			s.app = base
			version, expectedCode, kind := 1, CodeStorage, failuretype.TransactionWorkUnknown
			if mode == "after_validation" {
				expectedCode, kind = CodeInput, failuretype.RequestInvalid
			}
			if mode == "lost_completion" {
				version, expectedCode, kind = 2, CodeCommit, failuretype.TransactionCompletionUnknown
			}
			codeIs(t, err, expectedCode)
			takeKinds(t, report, kind)
			if after := snapshotUpgrade(t, s, version); !reflect.DeepEqual(after, before) {
				t.Fatal("migration rollback/uncertainty changed original values")
			}
			if err := s.run(background, func(tx *transaction) error { return validateState(tx, version, false) }, false); err != nil {
				t.Fatal("migration left partial layout")
			}
		})
	}
}
