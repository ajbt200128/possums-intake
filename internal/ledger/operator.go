package ledger

import (
	"context"
	"database/sql"
	"unicode/utf8"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"

	"github.com/pocketbase/ozzo-validation/v4/is"
	"github.com/pocketbase/pocketbase/core"
)

// OperatorCredentials are runtime-only and used only by explicit initialization.
// Never format or retain this value in diagnostics.
type OperatorCredentials struct{ Email, Password string }

func operatorFailure(constraint string) *Diagnostic {
	kind := failuretype.PolicyRefused
	if constraint == "runtime_email_and_password_8_to_71_bytes" {
		kind = failuretype.RequestInvalid
	}
	return &Diagnostic{Code: "LEDGER_OPERATOR_SETUP_INVALID", Stage: "operator_setup", Constraint: constraint, Outcome: "no_effect", kind: kind, NextStep: "Use disposable synthetic state. Supply valid runtime operator credentials only during explicit init --operator; serve never creates or resets a login."}
}

func (c OperatorCredentials) Validate() error {
	// Keep the native email validator, and stay below bcrypt's byte limit rather
	// than relying on PocketBase's multibyte truncation behavior.
	if len(c.Email) == 0 || len(c.Email) > 255 || is.EmailFormat.Validate(c.Email) != nil || !utf8.ValidString(c.Password) || utf8.RuneCountInString(c.Password) < 8 || len(c.Password) > 71 {
		return operatorFailure("runtime_email_and_password_8_to_71_bytes")
	}
	return nil
}

// InitOperator preserves Init's empty-directory refusal and transaction path.
func InitOperator(ctx context.Context, dir string, credentials OperatorCredentials) error {
	return InitOperatorObserved(ctx, dir, credentials, nil)
}

func InitOperatorObserved(ctx context.Context, dir string, credentials OperatorCredentials, observer failureowner.Observer) error {
	if err := credentials.Validate(); err != nil {
		failureowner.Observe(observer, FailureKind(err, failuretype.InitializationUnknown))
		return err
	}
	return initialize(ctx, dir, &credentials, observer)
}

// prepareOperator performs the SDK's synchronous password hashing before the
// bounded write transaction. The new record remains transient until saved there.
func prepareOperator(ctx context.Context, app core.App, credentials OperatorCredentials) (*core.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	collection, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.SetEmail(credentials.Email)
	record.SetPassword(credentials.Password)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return record, nil
}

func initializeOperator(tx *transaction, record *core.Record) error {
	app := tx.app
	collection, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		return err
	}
	collection.AuthAlert.Enabled = false
	if err := app.SaveWithContext(tx.ctx, collection); err != nil {
		return err
	}
	// Only auth options changed; the prepared record's field schema and table
	// identity remain valid. Save through the transaction app with SDK validation.
	return app.SaveWithContext(tx.ctx, record)
}

// OperatorApp exposes the SAME bootstrapped app to the trusted SDK router.
// It makes no storage writes, repairs or credential changes. Call before serving
// and drain HTTP before Close. Custom accounting continues through Service.
func (s *Service) OperatorApp() (core.App, error) {
	return s.OperatorAppObserved(nil)
}

func (s *Service) OperatorAppObserved(observer failureowner.Observer) (app core.App, result error) {
	defer func() {
		if recover() != nil {
			app = nil
			result = lifecycleFailure(&Diagnostic{}, failuretype.NativeReadUnknown)
		}
		if result != nil {
			failureowner.Observe(observer, FailureKind(result, failuretype.NativeReadUnknown))
		}
	}()
	if !s.Ready() {
		return nil, diagnostic(CodeClosed)
	}
	readFailure := func(err error) *Diagnostic {
		d := CanonicalFailure(&Diagnostic{kind: FailureKind(err, failuretype.NativeReadUnknown)}, failuretype.NativeReadUnknown)
		d.Outcome = "unavailable"
		return d
	}
	_, err := s.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err == sql.ErrNoRows {
		return nil, operatorFailure("native_superuser_collection_required")
	}
	if err != nil {
		return nil, readFailure(err)
	}
	// Stock admin writes intentionally bypass the project Record guard. Service
	// methods still validate input/state and retain their SQL/transaction rules.
	s.app.OnRecordValidate(AccountCollectionName).Unbind(accountValidationHook)
	return s.app, nil
}
