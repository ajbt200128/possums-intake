package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/types"
	_ "modernc.org/sqlite"
)

// Init creates empty disposable LOCAL state, without credits or superusers.
// A failure preserves partial files; neither Init nor Open repairs/resets them.
func Init(ctx context.Context, dir string) error {
	return initialize(ctx, dir, nil, nil)
}

// InitObserved adds only numeric error observation to explicit empty-state init.
func InitObserved(ctx context.Context, dir string, observer failureowner.Observer) error {
	return initialize(ctx, dir, nil, observer)
}

func initialize(ctx context.Context, dir string, operator *OperatorCredentials, observer failureowner.Observer) (result error) {
	owned := false
	defer func() {
		if recover() != nil {
			result = lifecycleFailure(&Diagnostic{}, failuretype.InitializationUnknown)
			owned = false
		}
		if result != nil && !owned {
			d := CanonicalFailure(result, failuretype.InitializationUnknown)
			if d.Code == CodeBoundary {
				d.Outcome = "unavailable"
			}
			result = d
			failureowner.Observe(observer, d.Kind())
		}
	}()
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
		return diagnostic(CodePath)
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return lifecycleFailure(err, failuretype.InitializationUnknown)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return lifecycleFailure(err, failuretype.InitializationUnknown)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return diagnostic(CodePath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return lifecycleFailure(err, failuretype.InitializationUnknown)
	}
	if len(entries) != 0 {
		return diagnostic(CodeExisting)
	}
	app, err := bootstrapObserved(dir, true, observer)
	if err != nil {
		owned = true // bootstrap owns its failure and any distinct teardown fault
		return err
	}
	defer func() {
		if err := clearStorage(app.ClearBootstrap, observer); err != nil && result == nil {
			result, owned = err, true
		}
	}()
	var preparedOperator *core.Record
	if operator != nil {
		preparedOperator, err = prepareOperator(ctx, app, *operator)
		if err != nil {
			return initFailure(err)
		}
	}
	s := newService(app)
	runCtx, report := WithFailureReport(ctx)
	err = s.run(runCtx, func(tx *transaction) error {
		if err := initializeSchema(tx); err != nil {
			return err
		}
		if preparedOperator != nil {
			return initializeOperator(tx, preparedOperator)
		}
		return nil
	}, false)
	ObserveRunFailures(observer, report, err, failuretype.InitializationUnknown)
	if err != nil {
		owned = true
		return initFailure(err)
	}
	runCtx, report = WithFailureReport(ctx)
	err = s.validate(runCtx)
	ObserveRunFailures(observer, report, err, failuretype.StorageValidationUnknown)
	if err != nil {
		owned = true
		return initFailure(err)
	}
	return nil
}

func initFailure(err error) *Diagnostic {
	d := *CanonicalFailure(err, failuretype.InitializationUnknown)
	d.Stage = "init"
	d.NextStep = "Initialization incomplete or unconfirmed. Preserve partial state; do not re-run init on it. Inspect privately or select a different empty disposable directory."
	return &d
}

// Open refuses missing/partial ledger state before PocketBase bootstrap can
// create a new data database. Never use Init as storage-loss recovery.
func Open(ctx context.Context, dir string) (*Service, error) {
	return OpenObserved(ctx, dir, nil)
}

func OpenObserved(ctx context.Context, dir string, observer failureowner.Observer) (*Service, error) {
	return openObserved(ctx, dir, observer, bootstrapObserved)
}

func openObserved(ctx context.Context, dir string, observer failureowner.Observer, bootstrapFn func(string, bool, failureowner.Observer) (*core.BaseApp, error)) (service *Service, result error) {
	owned := false
	defer func() {
		if recover() != nil {
			service = nil
			result = lifecycleFailure(&Diagnostic{}, failuretype.StorageOpenUnknown)
			owned = false
		}
		if result != nil && !owned {
			d := CanonicalFailure(result, failuretype.StorageOpenUnknown)
			if d.Code == CodeBoundary {
				d.Outcome = "unavailable"
			}
			result = d
			failureowner.Observe(observer, d.Kind())
		}
	}()
	if !filepath.IsAbs(dir) {
		return nil, diagnostic(CodePath)
	}
	info, err := os.Lstat(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, lifecycleFailure(err, failuretype.StorageOpenUnknown)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, diagnostic(CodeMissing)
	}
	for _, name := range []string{"data.db", "auxiliary.db"} {
		info, err = os.Lstat(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, lifecycleFailure(err, failuretype.StorageOpenUnknown)
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, diagnostic(CodeMissing)
		}
	}
	// Apply the same exact-layout checks before framework bootstrap can touch
	// incompatible disposable state. No version-specific legacy or upgrade path.
	if err := preflightObserved(ctx, dir, observer); err != nil {
		owned = true
		return nil, err
	}
	app, err := bootstrapFn(dir, false, observer)
	if err != nil {
		owned = true
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			if err := clearStorage(app.ClearBootstrap, observer); err != nil && result == nil {
				result, owned = err, true
			}
		}
	}()
	s := newService(app)
	runCtx, report := WithFailureReport(ctx)
	err = s.validate(runCtx)
	ObserveRunFailures(observer, report, err, failuretype.StorageValidationUnknown)
	if err != nil {
		owned = true
		return nil, err
	}
	accepted = true
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
	return bootstrapObserved(dir, create, nil)
}

func bootstrapObserved(dir string, create bool, observer failureowner.Observer) (*core.BaseApp, error) {
	return bootstrapUsingObserved(create, observer, func() *core.BaseApp { return storageApp(dir, create) })
}

// The private construction seam leaves Bootstrap and its primary/teardown owner
// identical for the default app and supported-hook qualification fixtures.
func bootstrapUsingObserved(create bool, observer failureowner.Observer, factory func() *core.BaseApp) (app *core.BaseApp, result error) {
	fallback := failuretype.StorageOpenUnknown
	if create {
		fallback = failuretype.InitializationUnknown
	}
	accepted := false
	defer func() {
		if recover() != nil {
			result = lifecycleFailure(&Diagnostic{}, fallback)
		}
		if !accepted {
			if result != nil {
				result = lifecycleFailure(result, fallback)
				failureowner.Observe(observer, FailureKind(result, fallback))
			}
			if app != nil {
				if err := clearStorage(app.ClearBootstrap, observer); err != nil && result == nil {
					result = err
				}
			}
			app = nil
		}
	}()
	app = factory()
	if err := app.Bootstrap(); err != nil {
		return app, err
	}
	accepted = true
	return app, nil
}

func storageApp(dir string, create bool) *core.BaseApp {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir: dir, QueryTimeout: WorkBudget, IsDev: false,
		DBConnect:        func(path string) (*dbx.DB, error) { return connect(path, create) },
		DataMaxOpenConns: 4, DataMaxIdleConns: 4, AuxMaxOpenConns: 1, AuxMaxIdleConns: 1,
	})
	app.Settings().Logs.MaxDays = 0
	bindAccountValidation(app)
	// Use supported SDK hooks to keep both saved settings and runtime logger
	// retention off, including native attempts to turn recording back on.
	app.OnModelValidate("_params").BindFunc(func(e *core.ModelEvent) error {
		if settings, ok := e.Model.(*core.Settings); ok {
			settings.Logs.MaxDays = 0
		}
		return e.Next()
	})
	app.OnSettingsUpdateRequest().BindFunc(func(e *core.SettingsUpdateRequestEvent) error {
		e.NewSettings.Logs.MaxDays = 0
		return e.Next()
	})
	// AuthAlert is login activity recording: the stock auth controller persists
	// IP/UA-derived origins before sending mail. Disable only that feature, not
	// MFA, OTP, OAuth or native admin management.
	app.OnCollectionValidate().Bind(&hook.Handler[*core.CollectionEvent]{
		Id: "intakeAuthActivityOff", Priority: -1000,
		Func: func(e *core.CollectionEvent) error {
			if e.Collection.IsAuth() {
				e.Collection.AuthAlert.Enabled = false
			}
			return e.Next()
		},
	})
	// Native SQL/restore may bypass collection validation. Suppress the SDK's
	// activity-origin writes and alert mail at their supported hooks too; this
	// neither rejects authentication nor emits login/activity counters.
	discardOriginWrite := func(*core.ModelEvent) error { return nil }
	app.OnModelCreate("_authOrigins").BindFunc(discardOriginWrite)
	app.OnModelUpdate("_authOrigins").BindFunc(discardOriginWrite)
	app.OnMailerRecordAuthAlertSend().BindFunc(func(*core.MailerRecordEvent) error { return nil })
	// This runs after loading settings but before the outer stock logger hook
	// processes them. No OnServe event/stock router/installer/cron is started.
	app.OnSettingsReload().Bind(&hook.Handler[*core.SettingsReloadEvent]{
		Func: func(e *core.SettingsReloadEvent) error {
			err := e.Next()
			e.App.Settings().Logs.MaxDays = 0
			return err
		}, Priority: 0,
	})
	return app
}

const schemaV3 = `
CREATE TABLE ledger_meta (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 schema_version INTEGER NOT NULL CHECK(schema_version=3),
 clock_high_water_ms INTEGER NOT NULL CHECK(typeof(clock_high_water_ms)='integer' AND clock_high_water_ms>=0 AND clock_high_water_ms<=9999999999999)
);
CREATE TABLE accounts (
 id TEXT PRIMARY KEY DEFAULT ('r'||lower(hex(randomblob(7)))) NOT NULL CHECK(length(id)=15 AND id NOT GLOB '*[^a-z0-9]*'),
 password TEXT DEFAULT '' NOT NULL,
 tokenKey TEXT DEFAULT '' NOT NULL,
 email TEXT DEFAULT '' NOT NULL,
 emailVisibility BOOLEAN DEFAULT FALSE NOT NULL,
 verified BOOLEAN DEFAULT FALSE NOT NULL,
 key_lookup TEXT DEFAULT '' NOT NULL,
 balance TEXT DEFAULT '0' NOT NULL CHECK(typeof(balance)='text' AND length(balance) BETWEEN 1 AND 20 AND balance NOT GLOB '*[^0-9]*' AND (balance='0' OR substr(balance,1,1) BETWEEN '1' AND '9') AND (length(balance)<20 OR balance<='18446744073709551615')),
 enabled INTEGER DEFAULT 1 NOT NULL CHECK(typeof(enabled)='integer' AND enabled IN (0,1))
);
CREATE TABLE ledger_reservations (
 reservation_id TEXT PRIMARY KEY NOT NULL CHECK(length(reservation_id)=32 AND reservation_id NOT GLOB '*[^0-9a-f]*'),
 idempotency_key TEXT NOT NULL UNIQUE,
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
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
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 amount TEXT NOT NULL CHECK(typeof(amount)='text' AND length(amount) BETWEEN 1 AND 20 AND amount NOT GLOB '*[^0-9]*' AND substr(amount,1,1) BETWEEN '1' AND '9'),
 balance_after TEXT NOT NULL CHECK(typeof(balance_after)='text' AND length(balance_after) BETWEEN 1 AND 20 AND balance_after NOT GLOB '*[^0-9]*' AND (balance_after='0' OR substr(balance_after,1,1) BETWEEN '1' AND '9')),
 committed_at INTEGER NOT NULL CHECK(typeof(committed_at)='integer' AND committed_at>0),
 retain_until INTEGER NOT NULL CHECK(typeof(retain_until)='integer' AND retain_until>committed_at)
);
CREATE UNIQUE INDEX ` + "`idx_tokenKey_ledgeraccounts2` ON `accounts` (`tokenKey`)" + `;
CREATE UNIQUE INDEX ` + "`idx_email_ledgeraccounts2` ON `accounts` (`email`) WHERE `email` != ''" + `;
CREATE UNIQUE INDEX accounts_key_lookup ON accounts (key_lookup);
CREATE INDEX ledger_reservations_account_active ON ledger_reservations(account_id,state,expires_at);
CREATE INDEX ledger_reservations_expiry ON ledger_reservations(state,expires_at);
CREATE INDEX ledger_reservations_prune ON ledger_reservations(retain_until) WHERE state!='active';
CREATE INDEX ledger_credits_prune ON ledger_credits(retain_until);
INSERT INTO ledger_meta(singleton,schema_version,clock_high_water_ms) VALUES(1,3,0);
`

const AccountCollectionID = "ledgeraccounts2"
const AccountCollectionName = "accounts"
const accountIndex = "CREATE UNIQUE INDEX accounts_key_lookup ON accounts (key_lookup)"

func accountCollection() *core.Collection {
	c := core.NewAuthCollection(AccountCollectionName, AccountCollectionID)
	c.AuthAlert.Enabled = false
	c.PasswordAuth.Enabled = true
	c.PasswordAuth.IdentityFields = []string{"key_lookup"}
	c.AuthRule = types.Pointer("")
	// Native customer tokens last twelve hours; website sessions retain their
	// independent one-hour absolute limit. Existing metadata is never upserted.
	c.AuthToken.Duration = 12 * 60 * 60
	c.Fields.GetByName("email").(*core.EmailField).Required = false
	c.Fields.Add(&core.TextField{Name: "key_lookup", Hidden: true, Required: true, Min: 67, Max: 67, Pattern: `^v1_[0-9a-f]{64}$`})
	c.Fields.Add(&core.TextField{Name: "balance", Required: true, Max: 20, Pattern: `^(0|[1-9][0-9]{0,19})$`})
	c.Fields.Add(&core.BoolField{Name: "enabled"}) // Required would prohibit false.
	c.Indexes = append(c.Indexes, accountIndex)
	return c
}

// AccountCollectionCompatible checks the fixed merged schema without changing
// SDK auth controls, credentials, financial data or collection metadata.
func AccountCollectionCompatible(c *core.Collection) bool {
	match, err := accountCollectionMatches(c)
	return err == nil && match
}

func accountCollectionMatches(c *core.Collection) (bool, error) {
	if c == nil {
		return false, nil
	}
	want := accountCollection()
	fields, err := json.Marshal(c.Fields)
	if err != nil {
		return false, err
	}
	expected, err := json.Marshal(want.Fields)
	if err != nil {
		return false, err
	}
	return string(fields) == string(expected) &&
		c.Id == want.Id && c.Name == want.Name && c.Type == want.Type && !c.System &&
		c.ListRule == nil && c.ViewRule == nil && c.CreateRule == nil && c.UpdateRule == nil && c.DeleteRule == nil && c.ManageRule == nil &&
		slices.Equal(c.Indexes, want.Indexes) && c.PasswordAuth.Enabled &&
		slices.Equal(c.PasswordAuth.IdentityFields, want.PasswordAuth.IdentityFields) &&
		c.AuthRule != nil && *c.AuthRule == "" && !c.AuthAlert.Enabled, nil
}

// initializeSchema registers the supported auth collection and adds financial
// constraints to its still-empty table, only during explicit fresh initialization.
// Existing state is never rebuilt, migrated or given replacement credentials.
func initializeSchema(tx *transaction) error {
	// Register ONLY through the supported collection save on fresh init. Never
	// retrofit collection metadata onto an existing financial table.
	var existing int
	if err := tx.query("SELECT count(*) FROM sqlite_schema WHERE lower(name) IN ('accounts','customer_keys','ledger_accounts')", nil).Row(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return diagnostic(CodeSchema)
	}
	if err := tx.app.SaveWithContext(tx.ctx, accountCollection()); err != nil {
		return err
	}
	var rows int
	if err := tx.query("SELECT count(*) FROM accounts", nil).Row(&rows); err != nil {
		return err
	}
	if rows != 0 {
		return diagnostic(CodeSchema)
	}
	if err := tx.exec("DROP TABLE accounts", nil); err != nil {
		return err
	}
	return tx.exec(schemaV3, nil)
}

func validateAccountCollection(tx *transaction) error {
	readFailure := func(err error) *Diagnostic {
		d := diagnostic(CodeStorage)
		d.kind = FailureKind(err, failuretype.StorageValidationUnknown)
		return d
	}
	c, err := tx.app.FindCollectionByNameOrId(AccountCollectionID)
	if err == sql.ErrNoRows {
		// The pinned collection lookup proves this required object is absent.
		return schemaFailure(schemaAccountMissing)
	}
	if err != nil {
		return readFailure(err)
	}
	match, err := accountCollectionMatches(c)
	if err != nil {
		return readFailure(err)
	}
	if !match {
		return schemaFailure(schemaAccountMetadata)
	}
	var count int
	if err := tx.query("SELECT count(*) FROM _collections WHERE lower(name) GLOB 'ledger_*' OR lower(name) IN ('accounts','customer_keys')", nil).Row(&count); err != nil {
		return readFailure(err)
	}
	if count != 1 {
		return schemaFailure(schemaAccountInventory)
	}
	return nil
}

func validateDDL(query func(string, dbx.Params) *dbx.Query) error {
	readFailure := func(err error) *Diagnostic {
		// Preflight has no transaction outcome. Retain only numeric cause
		// metadata; the transaction owner reconstructs its observed outcome.
		d := CanonicalFailure(&Diagnostic{kind: FailureKind(err, failuretype.StorageValidationUnknown)}, failuretype.StorageValidationUnknown)
		d.Outcome = "unavailable"
		return d
	}
	var objects int
	if err := query("SELECT count(*) FROM sqlite_schema WHERE (lower(name) GLOB 'ledger_*' OR lower(tbl_name) GLOB 'ledger_*' OR lower(name) IN ('accounts','customer_keys') OR lower(tbl_name) IN ('accounts','customer_keys')) AND sql IS NOT NULL", nil).Row(&objects); err != nil {
		return readFailure(err)
	}
	if objects != 11 {
		return schemaFailure(schemaObjectInventory)
	}
	for _, statement := range strings.Split(schemaV3, ";") {
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
		err := query("SELECT sql FROM sqlite_schema WHERE name={:name}", dbx.Params{"name": strings.Trim(name, "`")}).Row(&stored)
		if err == sql.ErrNoRows {
			return schemaFailure(schemaObjectMissing)
		}
		if err != nil {
			return readFailure(err)
		}
		if stored != statement {
			return schemaFailure(schemaObjectDefinition)
		}
	}
	if _, err := validatePaymentDDL(query); err != nil {
		if d, ok := err.(*Diagnostic); ok {
			return d
		}
		return readFailure(err)
	}
	return nil
}

func preflightLayout(ctx context.Context, dir string) error {
	return preflightObserved(ctx, dir, nil)
}

func preflightObserved(ctx context.Context, dir string, observer failureowner.Observer) (result error) {
	owned := false
	defer func() {
		if recover() != nil {
			result = lifecycleFailure(&Diagnostic{}, failuretype.StorageValidationUnknown)
			owned = false
		}
		if result != nil && !owned {
			d := CanonicalFailure(result, failuretype.StorageValidationUnknown)
			d.Outcome = "unavailable"
			result = d
			failureowner.Observe(observer, d.Kind())
		}
	}()
	work, cancel := context.WithTimeout(ctx, WorkBudget)
	defer cancel()
	uri := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "data.db")}).String()
	db, err := dbx.Open("sqlite", uri+"?mode=ro")
	if err != nil {
		d := CanonicalFailure(&Diagnostic{kind: FailureKind(err, failuretype.StorageOpenUnknown)}, failuretype.StorageOpenUnknown)
		d.Outcome = "unavailable"
		return d
	}
	defer func() {
		if err := clearStorage(db.Close, observer); err != nil && result == nil {
			result, owned = err, true
		}
	}()
	query := func(statement string, params dbx.Params) *dbx.Query {
		return db.NewQuery(statement).Bind(params).WithContext(work)
	}
	if err := validateDDL(query); err != nil {
		return err
	}
	// Decode through the SDK model's supported dbx PostScan before bootstrap
	// can save settings or touch incompatible collection metadata.
	var collection core.Collection
	if err := query("SELECT * FROM _collections WHERE id={:id}", dbx.Params{"id": AccountCollectionID}).One(&collection); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schemaFailure(schemaAccountMissing)
		}
		return err
	}
	match, err := accountCollectionMatches(&collection)
	if err != nil {
		return err
	}
	if !match {
		return schemaFailure(schemaAccountMetadata)
	}
	var collections int
	if err := query("SELECT count(*) FROM _collections WHERE lower(name) GLOB 'ledger_*' OR lower(name) IN ('accounts','customer_keys')", nil).Row(&collections); err != nil {
		return err
	}
	if collections != 1 {
		return schemaFailure(schemaAccountInventory)
	}
	return nil
}

// validate uses the same scoped transaction as ordinary accounting. No silent
// migrations of unknown state. Only the fresh merged version 3 is accepted.
func (s *Service) validate(ctx context.Context) error {
	return s.run(ctx, func(tx *transaction) error {
		readFailure := func(err error) *Diagnostic {
			d := diagnostic(CodeStorage)
			d.kind = FailureKind(err, failuretype.StorageValidationUnknown)
			return d
		}
		for _, p := range []struct {
			name     string
			expected any
		}{
			{"journal_mode", "wal"}, {"synchronous", int64(2)}, {"foreign_keys", int64(1)},
			{"busy_timeout", int64(BusyWaitMS)}, {"temp_store", int64(2)},
		} {
			var got any
			if err := tx.query("PRAGMA "+p.name, nil).Row(&got); err != nil {
				return readFailure(err)
			}
			if got != p.expected {
				return schemaFailure(schemaConnectionSettings)
			}
		}
		var violations []struct {
			Table  string        `db:"table"`
			RowID  sql.NullInt64 `db:"rowid"`
			Parent string        `db:"parent"`
			FKID   int           `db:"fkid"`
		}
		if err := tx.query("PRAGMA foreign_key_check", nil).All(&violations); err != nil {
			return readFailure(err)
		}
		if len(violations) > 0 {
			return diagnostic(CodeCorrupt)
		}
		var integrity string
		if err := tx.query("PRAGMA quick_check", nil).Row(&integrity); err != nil {
			return readFailure(err)
		}
		if integrity != "ok" {
			return diagnostic(CodeCorrupt)
		}
		// Schema version alone cannot certify missing constraints or indexes.
		// Compare the versioned financial/auth DDL, including SDK auth indexes.
		if err := validateDDL(tx.query); err != nil {
			return err
		}
		if err := validateAccountCollection(tx); err != nil {
			return err
		}
		if err := tx.clock(); err != nil {
			return CanonicalFailure(err, failuretype.StorageValidationUnknown)
		}
		var accounts []Account
		if err := tx.query("SELECT id AS account_id,balance,enabled FROM accounts ORDER BY id", nil).All(&accounts); err != nil {
			return readFailure(err)
		}
		for _, a := range accounts {
			if err := validateAccount(a); err != nil {
				return err
			}
			if _, _, err := tx.active(a, false); err != nil {
				return CanonicalFailure(err, failuretype.StorageValidationUnknown)
			}
		}
		var reservations []Reservation
		if err := tx.query("SELECT "+reservationColumns+" FROM ledger_reservations", nil).All(&reservations); err != nil {
			return readFailure(err)
		}
		for _, r := range reservations {
			if err := validateReservation(r); err != nil {
				return err
			}
		}
		var credits []Credit
		if err := tx.query("SELECT "+creditColumns+" FROM ledger_credits", nil).All(&credits); err != nil {
			return readFailure(err)
		}
		for _, c := range credits {
			if err := validateCredit(c); err != nil {
				return err
			}
		}
		if err := tx.validatePaymentRows(); err != nil {
			return CanonicalFailure(err, failuretype.StorageValidationUnknown)
		}
		return nil
	}, false)
}

// Close waits for the actual in-flight transaction to finish, including commit.
// It never abandons work or releases gate ownership on a timer.
func (s *Service) Close() error {
	return s.CloseObserved(nil)
}

func (s *Service) CloseObserved(observer failureowner.Observer) error {
	return clearStorage(s.close, observer)
}

func (s *Service) close() error {
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
		return err // clearStorage owns typed causes and separate close failures
	}
	return nil
}
func (s *Service) Ready() bool { return s.ready.Load() }

// The lifecycle packet owns scheduling Cleanup every 10s, with prune once per
// 60s. Open intentionally starts no maintenance ticks or stock OnServe event.
