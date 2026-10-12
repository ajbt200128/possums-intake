package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/hook"
	_ "modernc.org/sqlite"
)

// Init creates empty disposable LOCAL state, without credits or superusers.
// A failure preserves partial files; neither Init nor Open repairs/resets them.
func Init(ctx context.Context, dir string) error {
	return initialize(ctx, dir, nil)
}

func initialize(ctx context.Context, dir string, operator *OperatorCredentials) (result error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
		return diagnostic(CodePath)
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return diagnostic(CodeStartup)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return diagnostic(CodePath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return diagnostic(CodeStartup)
	}
	if len(entries) != 0 {
		return diagnostic(CodeExisting)
	}
	app, err := bootstrap(dir, true)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.ClearBootstrap(); err != nil && result == nil {
			result = diagnostic(CodeClose)
		}
	}()
	s := newService(app)
	if err := s.run(ctx, func(tx *transaction) error {
		if err := migrateV1(tx); err != nil {
			return err
		}
		if operator != nil {
			return initializeOperator(tx, *operator)
		}
		return nil
	}, false); err != nil {
		return initFailure(err)
	}
	if err := s.validate(ctx); err != nil {
		return initFailure(err)
	}
	return nil
}

func initFailure(err error) *Diagnostic {
	d := *Failure(err)
	d.Stage = "init"
	d.NextStep = "Initialization incomplete or unconfirmed. Preserve partial state; do not re-run init on it. Inspect privately or select a different empty disposable directory."
	return &d
}

// Open refuses missing/partial ledger state before PocketBase bootstrap can
// create a new data database. Never use Init as storage-loss recovery.
func Open(ctx context.Context, dir string) (*Service, error) {
	if !filepath.IsAbs(dir) {
		return nil, diagnostic(CodePath)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, diagnostic(CodeMissing)
	}
	for _, name := range []string{"data.db", "auxiliary.db"} {
		info, err = os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, diagnostic(CodeMissing)
		}
	}
	// Apply the same exact-layout checks before framework bootstrap can touch
	// incompatible disposable state. No version-specific legacy or upgrade path.
	if err := preflightLayout(ctx, dir); err != nil {
		return nil, err
	}
	app, err := bootstrap(dir, false)
	if err != nil {
		return nil, err
	}
	s := newService(app)
	if err := s.validate(ctx); err != nil {
		app.ClearBootstrap()
		return nil, err
	}
	return s, nil
}

func connect(path string, create bool) (*dbx.DB, error) {
	mode := "rw"
	if create {
		mode = "rwc"
	}
	// DSN pragmas run on EVERY physical connection, including newly opened pool
	// connections. Keep the upstream defensive/cache/journal-size settings.
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	return dbx.Open("sqlite", uri+"?mode="+mode+"&_pragma=busy_timeout(250)&_pragma=journal_mode(WAL)&_pragma=journal_size_limit(200000000)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32000)&_defensive=1")
}
func bootstrap(dir string, create bool) (*core.BaseApp, error) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir: dir, QueryTimeout: WorkBudget, IsDev: false,
		DBConnect:        func(path string) (*dbx.DB, error) { return connect(path, create) },
		DataMaxOpenConns: 4, DataMaxIdleConns: 4, AuxMaxOpenConns: 1, AuxMaxIdleConns: 1,
	})
	app.Settings().Logs.MaxDays = 0
	bindAccountValidation(app)
	// This runs after loading settings but before the outer stock logger hook
	// processes them. No OnServe event/stock router/installer/cron is started.
	app.OnSettingsReload().Bind(&hook.Handler[*core.SettingsReloadEvent]{
		Func: func(e *core.SettingsReloadEvent) error {
			err := e.Next()
			e.App.Settings().Logs.MaxDays = 0
			return err
		}, Priority: 0,
	})
	if err := app.Bootstrap(); err != nil {
		app.ClearBootstrap()
		return nil, diagnostic(CodeStartup)
	}
	return app, nil
}

const schemaV1 = `
CREATE TABLE ledger_meta (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 schema_version INTEGER NOT NULL CHECK(schema_version=1),
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
INSERT INTO ledger_meta(singleton,schema_version,clock_high_water_ms) VALUES(1,1,0);
`

const AccountCollectionID = "ledgeraccounts1"
const AccountCollectionName = "ledger_accounts"
const accountIndex = "CREATE UNIQUE INDEX ledger_accounts_account_id ON ledger_accounts (account_id)"

func accountCollection() *core.Collection {
	c := core.NewBaseCollection(AccountCollectionName, AccountCollectionID)
	c.Fields.Add(&core.TextField{Name: "account_id", Required: true, Max: 128})
	c.Fields.Add(&core.TextField{Name: "balance", Required: true, Max: 20, Pattern: `^(0|[1-9][0-9]{0,19})$`})
	c.Fields.Add(&core.BoolField{Name: "enabled"}) // Required would prohibit false.
	c.Indexes = append(c.Indexes, accountIndex)
	return c
}

// migrateV1 creates the first unreleased native-account layout during explicit
// empty-state init only; it is not an existing-state upgrade or rebuild.
func migrateV1(tx *transaction) error {
	// Register ONLY through the supported collection save on fresh init. Never
	// retrofit collection metadata onto an existing financial table.
	var existing int
	if err := tx.query("SELECT count(*) FROM sqlite_schema WHERE name='ledger_accounts'", nil).Row(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return diagnostic(CodeSchema)
	}
	if err := tx.app.SaveWithContext(tx.ctx, accountCollection()); err != nil {
		return err
	}
	var rows int
	if err := tx.query("SELECT count(*) FROM ledger_accounts", nil).Row(&rows); err != nil {
		return err
	}
	if rows != 0 {
		return diagnostic(CodeSchema)
	}
	if err := tx.exec("DROP TABLE ledger_accounts", nil); err != nil {
		return err
	}
	return tx.exec(schemaV1, nil)
}

func validateAccountCollection(tx *transaction) error {
	c, err := tx.app.FindCollectionByNameOrId(AccountCollectionID)
	if err != nil {
		return diagnostic(CodeSchema)
	}
	want := accountCollection()
	fields, e1 := json.Marshal(c.Fields)
	expected, e2 := json.Marshal(want.Fields)
	if e1 != nil || e2 != nil || string(fields) != string(expected) || c.Id != want.Id || c.Name != want.Name || c.Type != want.Type || c.System || c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil || len(c.Indexes) != 1 || c.Indexes[0] != accountIndex {
		return diagnostic(CodeSchema)
	}
	var count int
	if err := tx.query("SELECT count(*) FROM _collections WHERE name LIKE 'ledger_%'", nil).Row(&count); err != nil || count != 1 {
		return diagnostic(CodeSchema)
	}
	return nil
}

func validateDDL(query func(string, dbx.Params) *dbx.Query) error {
	var objects int
	if err := query("SELECT count(*) FROM sqlite_schema WHERE (name LIKE 'ledger_%' OR tbl_name LIKE 'ledger_%') AND sql IS NOT NULL", nil).Row(&objects); err != nil || objects != 9 {
		return diagnostic(CodeSchema)
	}
	for _, statement := range strings.Split(schemaV1, ";") {
		statement = strings.TrimSpace(statement)
		if !strings.HasPrefix(statement, "CREATE ") {
			continue
		}
		parts := strings.Fields(statement)
		name := parts[2]
		if parts[1] == "UNIQUE" {
			name = parts[3]
		}
		var stored string
		if err := query("SELECT sql FROM sqlite_schema WHERE name={:name}", dbx.Params{"name": name}).Row(&stored); err != nil || stored != statement {
			return diagnostic(CodeSchema)
		}
	}
	return nil
}

func preflightLayout(ctx context.Context, dir string) error {
	work, cancel := context.WithTimeout(ctx, WorkBudget)
	defer cancel()
	uri := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "data.db")}).String()
	db, err := dbx.Open("sqlite", uri+"?mode=ro")
	if err != nil {
		return diagnostic(CodeSchema)
	}
	defer db.Close()
	return validateDDL(func(statement string, params dbx.Params) *dbx.Query {
		return db.NewQuery(statement).Bind(params).WithContext(work)
	})
}

// validate uses the same scoped transaction as ordinary accounting. No silent
// migrations of unknown state. Version 1 is the first unreleased native layout;
// earlier disposable layouts with the same version still fail exact checks.
func (s *Service) validate(ctx context.Context) error {
	return s.run(ctx, func(tx *transaction) error {
		for _, p := range []struct {
			name     string
			expected any
		}{
			{"journal_mode", "wal"}, {"synchronous", int64(2)}, {"foreign_keys", int64(1)},
			{"busy_timeout", int64(BusyWaitMS)}, {"temp_store", int64(2)},
		} {
			var got any
			if err := tx.query("PRAGMA "+p.name, nil).Row(&got); err != nil || got != p.expected {
				return diagnostic(CodeSchema)
			}
		}
		var violations []struct {
			Table  string        `db:"table"`
			RowID  sql.NullInt64 `db:"rowid"`
			Parent string        `db:"parent"`
			FKID   int           `db:"fkid"`
		}
		if err := tx.query("PRAGMA foreign_key_check", nil).All(&violations); err != nil {
			return diagnostic(CodeSchema)
		}
		if len(violations) > 0 {
			return diagnostic(CodeCorrupt)
		}
		var integrity string
		if err := tx.query("PRAGMA quick_check", nil).Row(&integrity); err != nil || integrity != "ok" {
			return diagnostic(CodeCorrupt)
		}
		// Schema version alone cannot certify missing constraints or indexes.
		// Compare our nine versioned DDL statements, not framework schema.
		if err := validateDDL(tx.query); err != nil {
			return err
		}
		if err := validateAccountCollection(tx); err != nil {
			return err
		}
		if err := tx.clock(); err != nil {
			return err
		}
		var accounts []Account
		if err := tx.query("SELECT account_id,balance,enabled FROM ledger_accounts ORDER BY account_id", nil).All(&accounts); err != nil {
			return diagnostic(CodeSchema)
		}
		for _, a := range accounts {
			if err := validateAccount(a); err != nil {
				return err
			}
			if _, _, err := tx.active(a, false); err != nil {
				return err
			}
		}
		var reservations []Reservation
		if err := tx.query("SELECT "+reservationColumns+" FROM ledger_reservations", nil).All(&reservations); err != nil {
			return diagnostic(CodeSchema)
		}
		for _, r := range reservations {
			if err := validateReservation(r); err != nil {
				return err
			}
		}
		var credits []Credit
		if err := tx.query("SELECT "+creditColumns+" FROM ledger_credits", nil).All(&credits); err != nil {
			return diagnostic(CodeSchema)
		}
		for _, c := range credits {
			if err := validateCredit(c); err != nil {
				return err
			}
		}
		return nil
	}, false)
}

// Close waits for the actual in-flight transaction to finish, including commit.
// It never abandons work or releases gate ownership on a timer.
func (s *Service) Close() error {
	s.ready.Store(false)
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	// Trigger the supported core termination hook without invoking OnServe,
	// the stock router, installer or cron. Preserve closure errors instead of
	// the PocketBase CLI's best-effort termination default.
	event := &core.TerminateEvent{App: s.app}
	if err := s.app.OnTerminate().Trigger(event, func(e *core.TerminateEvent) error {
		return e.App.ClearBootstrap()
	}); err != nil {
		return diagnostic(CodeClose)
	}
	return nil
}
func (s *Service) Ready() bool { return s.ready.Load() }

// The lifecycle packet owns scheduling Cleanup every 10s, with prune once per
// 60s. Open intentionally starts no maintenance ticks or stock OnServe event.
