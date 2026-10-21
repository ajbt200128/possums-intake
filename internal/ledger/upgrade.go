package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
)

// Upgrade explicitly upgrades an existing, stopped, exact supported v1 ledger.
// It never initializes, repairs, expires holds or changes credentials. This is
// one-way maintenance, not serve-time migration. Valid v2 reruns return nil
// unchanged, including the accounting clock watermark.
func Upgrade(ctx context.Context, dir string) error {
	return UpgradeObserved(ctx, dir, nil)
}

func UpgradeObserved(ctx context.Context, dir string, observer failureowner.Observer) (result error) {
	owned := false
	defer func() {
		if recover() != nil {
			result = lifecycleFailure(&Diagnostic{}, failuretype.StorageValidationUnknown)
			owned = false
		}
		if result != nil && !owned {
			result = CanonicalFailure(result, failuretype.StorageValidationUnknown)
			failureowner.Observe(observer, FailureKind(result, failuretype.StorageValidationUnknown))
		}
	}()
	if err := existingStateDirectory(dir); err != nil {
		return err
	}
	if err := preflightVersionObserved(ctx, dir, observer, true); err != nil {
		owned = true
		return err
	}
	app, err := bootstrapObserved(dir, false, observer)
	if err != nil {
		owned = true
		return err
	}
	defer func() {
		if err := clearStorage(app.ClearBootstrap, observer); err != nil && result == nil {
			result, owned = err, true
		}
	}()
	s := newService(app)
	runCtx, report := WithFailureReport(ctx)
	err = s.run(runCtx, upgradeTransaction, false)
	ObserveRunFailures(observer, report, err, failuretype.StorageValidationUnknown)
	if err != nil {
		owned = true
	}
	return err
}

func upgradeTransaction(tx *transaction) error {
	var version int
	if err := tx.query("SELECT schema_version FROM ledger_meta WHERE singleton=1", nil).Row(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return diagnostic(CodeSchema)
		}
		return err
	}
	// Validate original rows, keys, terminal evidence, holds, native metadata,
	// integrity and exact DDL BEFORE the first ledger mutation. Do not advance
	// the watermark, even for a successful v2 no-op.
	if err := validateState(tx, version, false); err != nil {
		return err
	}
	if version == 2 {
		return nil
	}
	if err := upgradeV1(tx); err != nil {
		return err
	}
	return validateState(tx, 2, false)
}

func upgradeV1(tx *transaction) error {
	// SQLite rename can rewrite unrelated views/FKs. Never commit changes to
	// other schema objects, even when native administration has added them.
	type schemaObject struct {
		Name string `db:"name"`
		SQL  string `db:"sql"`
	}
	readOtherDDL := func() ([]schemaObject, error) {
		var objects []schemaObject
		err := tx.query("SELECT name,sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT IN ('ledger_meta','ledger_reservations') AND tbl_name!='ledger_reservations' ORDER BY name", nil).All(&objects)
		return objects, err
	}
	before, err := readOtherDDL()
	if err != nil {
		return err
	}
	// Rebuild only these two tables with checks/FKs enabled, using exactly the
	// same DDL as fresh initialization. Any dependent native schema must refuse
	// atomically rather than leave rewritten references or cascading changes.
	for _, table := range []string{"ledger_meta", "ledger_reservations"} {
		if err := tx.exec("ALTER TABLE "+table+" RENAME TO "+table+"_upgrade_old", nil); err != nil {
			return err
		}
	}
	for _, statement := range strings.Split(schemaV2, ";") {
		statement = strings.TrimSpace(statement)
		if strings.HasPrefix(statement, "CREATE TABLE ledger_meta (") || strings.HasPrefix(statement, "CREATE TABLE ledger_reservations (") {
			if err := tx.exec(statement, nil); err != nil {
				return err
			}
		}
	}
	if err := tx.exec("INSERT INTO ledger_meta(singleton,schema_version,clock_high_water_ms) SELECT singleton,2,clock_high_water_ms FROM ledger_meta_upgrade_old", nil); err != nil {
		return err
	}
	legacyColumns := strings.Replace(reservationColumns, "initial_expires_at", "expires_at AS initial_expires_at", 1)
	if err := tx.exec("INSERT INTO ledger_reservations("+reservationColumns+") SELECT "+legacyColumns+" FROM ledger_reservations_upgrade_old", nil); err != nil {
		return err
	}
	for _, table := range []string{"ledger_meta", "ledger_reservations"} {
		if err := tx.exec("DROP TABLE "+table+"_upgrade_old", nil); err != nil {
			return err
		}
	}
	for _, statement := range strings.Split(schemaV2, ";") {
		statement = strings.TrimSpace(statement)
		if strings.HasPrefix(statement, "CREATE INDEX ledger_reservations_") {
			if err := tx.exec(statement, nil); err != nil {
				return err
			}
		}
	}
	after, err := readOtherDDL()
	if err != nil {
		return err
	}
	if len(before) != len(after) {
		return diagnostic(CodeSchema)
	}
	for i := range before {
		if before[i] != after[i] {
			return diagnostic(CodeSchema)
		}
	}
	return nil
}
