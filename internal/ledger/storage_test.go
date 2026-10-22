package ledger

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/pocketbase/dbx"
)

func TestExplicitInitMissingPartialAndUnknownSchema(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := Open(background, missing)
	codeIs(t, err, CodeMissing)
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("serve created a missing directory")
	}
	if e := Init(background, "relative"); e == nil {
		t.Fatal("relative init accepted")
	}
	empty := t.TempDir()
	_, err = Open(background, empty)
	codeIs(t, err, CodeMissing)
	files, _ := os.ReadDir(empty)
	if len(files) != 0 {
		t.Fatal("serve initialized empty state")
	}
	partial := t.TempDir()
	if err := os.WriteFile(filepath.Join(partial, "data.db"), []byte("SYNTHETIC_PARTIAL"), 0600); err != nil {
		t.Fatal("fixture write failed")
	}
	codeIs(t, Init(background, partial), CodeExisting)
	_, err = Open(background, partial)
	codeIs(t, err, CodeMissing)
	s, dir, _ := testService(t)
	codeIs(t, Init(background, dir), CodeExisting)
	if err := s.run(background, func(tx *transaction) error { return tx.exec("DELETE FROM ledger_meta", nil) }, false); err != nil {
		t.Fatal("fixture mutation failed")
	}
	s.Close()
	_, err = Open(background, dir)
	codeIs(t, err, CodeSchema)
	// Failed Open preserves money tables instead of silently running our init.
	db, e := connect(filepath.Join(dir, "data.db"), false)
	if e != nil {
		t.Fatal("inspection failed")
	}
	defer db.Close()
	var rows int
	if e := db.NewQuery("SELECT count(*) FROM ledger_meta").Row(&rows); e != nil || rows != 0 {
		t.Fatal("missing metadata reset")
	}
}
func TestSchemaDriftRejectedAndMigrationRollback(t *testing.T) {
	s, dir, _ := testService(t)
	if e := s.run(background, func(tx *transaction) error { return tx.exec("DROP INDEX ledger_reservations_expiry", nil) }, false); e != nil {
		t.Fatal("fixture schema mutation failed")
	}
	s.Close()
	_, e := Open(background, dir)
	codeIs(t, e, CodeSchema)
	// Run the actual versioned migration against partial state; duplicate-object
	// failure rolls all earlier DDL back rather than producing a ready schema.
	fresh := filepath.Join(t.TempDir(), "partial")
	os.Mkdir(fresh, 0700)
	app, e := bootstrap(fresh, true)
	if e != nil {
		t.Fatal("fixture bootstrap failed")
	}
	defer app.ClearBootstrap()
	partial := newService(app)
	if e := partial.run(background, func(tx *transaction) error { return tx.exec("CREATE TABLE accounts (account_id TEXT)", nil) }, false); e != nil {
		t.Fatal("fixture table failed")
	}
	e = partial.run(background, initializeSchema, false)
	codeIs(t, e, CodeSchema)
	var count int
	if e := app.DB().NewQuery("SELECT count(*) FROM sqlite_schema WHERE name='ledger_meta'").Row(&count); e != nil || count != 0 {
		t.Fatal("partial migration DDL committed")
	}
}
func TestConnectionPragmasAndForeignKeys(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "10", now)
	// Hold all four physical read connections simultaneously; closing/reopening
	// the pool verifies DSN pragmas apply to NEW connections, not only bootstrap.
	pool := s.app.ConcurrentDB().(*dbx.DB).DB()
	for pass := 0; pass < 2; pass++ {
		var connections []*sql.Conn
		for i := 0; i < 4; i++ {
			conn, e := pool.Conn(background)
			if e != nil {
				t.Fatal("pool acquire failed")
			}
			connections = append(connections, conn)
			for _, p := range []struct {
				sql   string
				value any
			}{{"PRAGMA journal_mode", "wal"}, {"PRAGMA synchronous", int64(2)}, {"PRAGMA foreign_keys", int64(1)}, {"PRAGMA busy_timeout", int64(250)}, {"PRAGMA temp_store", int64(2)}} {
				var got any
				if e := conn.QueryRowContext(background, p.sql).Scan(&got); e != nil || got != p.value {
					t.Fatal("per-connection pragma differs")
				}
			}
		}
		for _, conn := range connections {
			conn.Close()
		}
		pool.SetMaxIdleConns(0)
		pool.SetMaxIdleConns(4)
	}
	r := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "1", 600, key(now, 2)})
	for _, statement := range []string{
		"DELETE FROM accounts WHERE id='aaaaaaaaaaaaaaa'",
		"INSERT INTO ledger_credits(idempotency_key,account_id,amount,balance_after,committed_at,retain_until) VALUES('synthetic000000','missing','1','1',1,2)",
		"UPDATE ledger_reservations SET state='expired' WHERE account_id='aaaaaaaaaaaaaaa'",
	} {
		e := s.run(background, func(tx *transaction) error { return tx.exec(statement, nil) }, true)
		codeIs(t, e, CodeStorage)
	}
	b := balance(t, s, "aaaaaaaaaaaaaaa")
	if b.AccountBalance.Balance != "10" || len(b.PendingReservations) != 1 || b.PendingReservations[0].ReservationID != r.ReservationID {
		t.Fatal("constraint violation changed state")
	}
	var numeric string
	if e := s.run(background, func(tx *transaction) error {
		return tx.query("SELECT typeof(balance) FROM accounts WHERE id='aaaaaaaaaaaaaaa'", nil).Row(&numeric)
	}, true); e != nil || numeric != "text" {
		t.Fatal("money stored as SQLite numeric")
	}
}
func TestCorruptStateFailsClosedWithoutRepair(t *testing.T) {
	for _, value := range []string{"18446744073709551616", "01", "-1"} {
		t.Run(value, func(t *testing.T) {
			s, dir, now := testService(t)
			fund(t, s, "aaaaaaaaaaaaaaa", "10", now)
			r := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "5", 1, key(now, 2)})
			if e := s.run(background, func(tx *transaction) error {
				if e := tx.exec("PRAGMA ignore_check_constraints=ON", nil); e != nil {
					return e
				}
				if e := tx.exec("UPDATE accounts SET balance={:value} WHERE id='aaaaaaaaaaaaaaa'", dbx.Params{"value": value}); e != nil {
					return e
				}
				return tx.exec("PRAGMA ignore_check_constraints=OFF", nil)
			}, false); e != nil {
				t.Fatal("synthetic corruption fixture failed")
			}
			advance(t, s, int64(r.ExpiresAt))
			_, e := s.Balance(background, "aaaaaaaaaaaaaaa")
			codeIs(t, e, CodeCorrupt)
			if s.Ready() {
				t.Fatal("corrupt state remained ready")
			}
			s.Close()
			_, e = Open(background, dir)
			codeIs(t, e, CodeCorrupt)
			db, e := connect(filepath.Join(dir, "data.db"), false)
			if e != nil {
				t.Fatal("inspection failed")
			}
			defer db.Close()
			var state string
			if e := db.NewQuery("SELECT state FROM ledger_reservations WHERE reservation_id={:id}").Bind(dbx.Params{"id": r.ReservationID}).Row(&state); e != nil || state != "active" {
				t.Fatal("expiry concealed corruption by changing hold")
			}
		})
	}
	// Canonical amounts alone do not establish a possible total/hold sum.
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "10", now)
	r := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "8", 1, key(now, 2)})
	if e := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE accounts SET balance='7' WHERE id='aaaaaaaaaaaaaaa'", nil)
	}, false); e != nil {
		t.Fatal("fixture mutation failed")
	}
	advance(t, s, int64(r.ExpiresAt))
	_, e := s.Lookup(background, r.ReservationID)
	codeIs(t, e, CodeCorrupt)
}
func TestRestartPreservesAcknowledgedBalancesActiveAndTerminal(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	first := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "30", 600, key(now, 2)})
	terminal, e := s.Settle(background, SettleInput{first.ReservationID, "17"})
	must(t, terminal, e)
	active := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "20", 600, key(now, 3)})
	before := balance(t, s, "aaaaaaaaaaaaaaa")
	if e := s.Close(); e != nil {
		t.Fatal("close failed")
	}
	reopened, e := Open(background, dir)
	must(t, reopened, e)
	defer reopened.Close()
	after := balance(t, reopened, "aaaaaaaaaaaaaaa")
	if before.AccountBalance != after.AccountBalance || after.Available != "63" || len(after.PendingReservations) != 1 || after.PendingReservations[0].ReservationID != active.ReservationID {
		t.Fatal("restart lost acknowledged state")
	}
	row, e := reopened.Lookup(background, terminal.ReservationID)
	must(t, row, e)
	if row.State != Settled || *row.ChargedAmount != "17" {
		t.Fatal("restart lost terminal receipt")
	}
	row, e = reopened.Settle(background, SettleInput{terminal.ReservationID, "30"})
	must(t, row, e)
	if *row.ChargedAmount != "17" || balance(t, reopened, "aaaaaaaaaaaaaaa").AccountBalance.Balance != "83" {
		t.Fatal("restart duplicate charged again")
	}
}
func TestOnlyAccountCollectionNoSuperusersOrActivityLogs(t *testing.T) {
	s, dir, _ := testService(t)
	var superusers, collections int
	if e := s.run(background, func(tx *transaction) error {
		if e := tx.query("SELECT count(*) FROM _superusers", nil).Row(&superusers); e != nil {
			return e
		}
		return tx.query("SELECT count(*) FROM _collections WHERE name='accounts'", nil).Row(&collections)
	}, true); e != nil || superusers != 0 || collections != 1 {
		t.Fatal("bootstrap account collection or superuser policy differs")
	}
	// Store a synthetic old configuration that would enable upstream logging.
	// Reload hook must override it before the stock logger sees the settings.
	s.app.Settings().Logs.MaxDays = 5
	if e := s.app.Save(s.app.Settings()); e != nil {
		t.Fatal("synthetic settings fixture failed")
	}
	if s.app.Settings().Logs.MaxDays != 0 {
		t.Fatal("settings reload enabled activity logging")
	}
	s.Close()
	reopened, e := Open(context.Background(), dir)
	must(t, reopened, e)
	if reopened.app.Settings().Logs.MaxDays != 0 {
		t.Fatal("restart enabled stored logging default")
	}
	reopened.app.Logger().Error("SYNTHETIC_LOG_SENTINEL")
	if e := reopened.Close(); e != nil {
		t.Fatal("close failed")
	}
	aux, e := connect(filepath.Join(dir, "auxiliary.db"), false)
	if e != nil {
		t.Fatal("aux inspection failed")
	}
	defer aux.Close()
	var logs int
	if e := aux.NewQuery("SELECT count(*) FROM _logs").Row(&logs); e != nil || logs != 0 {
		t.Fatal("synthetic activity was persisted")
	}
}
