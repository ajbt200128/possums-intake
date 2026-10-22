package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	validation "github.com/pocketbase/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/crypto/bcrypt"
	"possums-intake/internal/failuretype"
)

func operatorPreparationApp(t *testing.T) *core.BaseApp {
	t.Helper()
	app, err := bootstrap(t.TempDir(), true)
	if err != nil {
		t.Fatal("preparation fixture bootstrap unavailable")
	}
	t.Cleanup(func() {
		if app.ClearBootstrap() != nil {
			t.Error("preparation fixture cleanup unavailable")
		}
	})
	return app
}

func assertOperatorPreparationEmpty(t *testing.T, app core.App) {
	t.Helper()
	count, err := app.CountRecords(core.CollectionNameSuperusers)
	if err != nil || count != 0 {
		t.Fatal("transient preparation persisted credentials")
	}
	var objects int
	if app.DB().NewQuery("SELECT count(*) FROM sqlite_schema WHERE name='accounts'").Row(&objects) != nil || objects != 0 {
		t.Fatal("initialization schema escaped transaction")
	}
}

func TestOperatorPreparationNativeHashAndTransactionalSave(t *testing.T) {
	app := operatorPreparationApp(t)
	credentials := OperatorCredentials{Email: "prepared@example.invalid", Password: "synthetic-prepared-password"}
	record, err := prepareOperator(background, app, credentials)
	if err != nil || record == nil || !record.IsNew() {
		t.Fatal("native preparation unavailable")
	}
	hash := record.GetString("password:hash")
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost != bcrypt.DefaultCost || !record.ValidatePassword(credentials.Password) {
		t.Fatal("native preparation changed default password strength")
	}
	if record.GetString("password") != credentials.Password {
		t.Fatal("native preparation lost pending stock validation input")
	}
	assertOperatorPreparationEmpty(t, app)
	validated := false
	app.OnRecordValidate(core.CollectionNameSuperusers).BindFunc(func(e *core.RecordEvent) error {
		if e.App.TxInfo() == nil || e.Record != record {
			t.Fatal("prepared credential validation escaped transaction")
		}
		validated = true
		return e.Next()
	})
	s := newService(app)
	if s.run(background, func(tx *transaction) error {
		if err := initializeSchema(tx); err != nil {
			return err
		}
		fresh, err := tx.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
		if err != nil {
			return err
		}
		preparedFields, err := json.Marshal(record.Collection().Fields)
		if err != nil {
			return err
		}
		freshFields, err := json.Marshal(fresh.Fields)
		if err != nil {
			return err
		}
		if fresh.Id != record.Collection().Id || fresh.Name != record.Collection().Name || string(freshFields) != string(preparedFields) {
			t.Fatal("prepared collection metadata is incompatible")
		}
		return initializeOperator(tx, record)
	}, false) != nil {
		t.Fatal("prepared transactional initialization unavailable")
	}
	if !validated || record.GetString("password:hash") != hash || record.GetString("password") != "" {
		t.Fatal("native save skipped validation, rehashed, or retained plain input")
	}
	saved, err := app.FindFirstRecordByData(core.CollectionNameSuperusers, "email", credentials.Email)
	if err != nil || !saved.ValidatePassword(credentials.Password) {
		t.Fatal("saved native credential cannot authenticate")
	}
	if s.validate(background) != nil {
		t.Fatal("prepared initialization schema validation unavailable")
	}
}

func TestOperatorPreparationInvalidDataRollsBackSchemaAndCredentials(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		credentials OperatorCredentials
	}{
		{"password", OperatorCredentials{Email: "prepared@example.invalid", Password: "short"}},
		{"email", OperatorCredentials{Email: "invalid", Password: "synthetic-prepared-password"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			app := operatorPreparationApp(t)
			before, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
			if err != nil {
				t.Fatal("native collection fixture unavailable")
			}
			beforeJSON, err := json.Marshal(before)
			if err != nil {
				t.Fatal("native collection comparison unavailable")
			}
			// Deliberately bypass only our input check: SDK SaveWithContext must
			// still validate the prepared plain password and email itself.
			record, err := prepareOperator(background, app, fixture.credentials)
			if err != nil {
				t.Fatal("invalid native preparation unavailable")
			}
			assertOperatorPreparationEmpty(t, app)
			s := newService(app)
			var saveErr error
			err = s.run(background, func(tx *transaction) error {
				if err := initializeSchema(tx); err != nil {
					return err
				}
				saveErr = initializeOperator(tx, record)
				return saveErr
			}, false)
			var fields validation.Errors
			if err == nil || !errors.As(saveErr, &fields) || fields[fixture.name] == nil {
				t.Fatal("stock validation did not refuse invalid prepared data")
			}
			assertOperatorPreparationEmpty(t, app)
			after, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
			if err != nil {
				t.Fatal("rolled-back native collection unavailable")
			}
			afterJSON, err := json.Marshal(after)
			if err != nil || string(beforeJSON) != string(afterJSON) {
				t.Fatal("credential refusal committed native settings")
			}
		})
	}
}

func TestOperatorPreparationRootCancellation(t *testing.T) {
	app := operatorPreparationApp(t)
	credentials := OperatorCredentials{Email: "prepared@example.invalid", Password: "synthetic-prepared-password"}
	for _, during := range []bool{false, true} {
		ctx, cancel := context.WithCancel(background)
		lookup := false
		wrapped := &validationReadApp{App: app, find: func(name string) (*core.Collection, error) {
			lookup = true
			cancel()
			return app.FindCollectionByNameOrId(name)
		}}
		if !during {
			cancel()
		}
		record, err := prepareOperator(ctx, wrapped, credentials)
		cancel()
		if record != nil || err != context.Canceled || lookup != during {
			t.Fatal("preparation lost root cancellation boundary")
		}
		assertOperatorPreparationEmpty(t, app)
	}
	ctx, cancel := context.WithCancel(background)
	cancel()
	observer := &lifecycleObserver{}
	if err := InitOperatorObserved(ctx, filepath.Join(t.TempDir(), "state"), credentials, observer); err == nil || Failure(err).Kind() != failuretype.Cancelled {
		t.Fatal("operator initialization lost cancellation classification")
	}
	observer.want(t, failuretype.Cancelled)
}
