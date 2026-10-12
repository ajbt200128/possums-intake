package ledger

import (
	"context"
	"unicode/utf8"

	"github.com/pocketbase/ozzo-validation/v4/is"
	"github.com/pocketbase/pocketbase/core"
)

// OperatorCredentials are runtime-only and used only by explicit initialization.
// Never format or retain this value in diagnostics.
type OperatorCredentials struct{ Email, Password string }

func operatorFailure(constraint string) *Diagnostic {
	return &Diagnostic{Code: "LEDGER_OPERATOR_SETUP_INVALID", Stage: "operator_setup", Constraint: constraint, Outcome: "no_effect", NextStep: "Use disposable synthetic state. Supply valid runtime operator credentials only during explicit init --operator; serve never creates or resets a login."}
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
	if err := credentials.Validate(); err != nil {
		return err
	}
	return initialize(ctx, dir, &credentials)
}

func initializeOperator(tx *transaction, credentials OperatorCredentials) error {
	app := tx.app
	collection, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		return err
	}
	collection.AuthAlert.Enabled = false
	collection.OTP.Enabled = false
	collection.MFA.Enabled = false
	collection.OAuth2.Enabled = false
	collection.PasswordAuth.Enabled = true
	collection.PasswordAuth.IdentityFields = []string{"email"}
	if err := app.SaveWithContext(tx.ctx, collection); err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.SetEmail(credentials.Email)
	record.SetPassword(credentials.Password)
	return app.SaveWithContext(tx.ctx, record)
}

// OperatorApp exposes the SAME bootstrapped app to the restricted native router.
// It makes no writes, repairs or credential changes. Call before serving, and
// drain HTTP before Close. Accounting must continue through Service, not app.
func (s *Service) OperatorApp() (core.App, error) {
	if !s.Ready() {
		return nil, diagnostic(CodeClosed)
	}
	c, err := s.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil || c.AuthAlert.Enabled || c.OTP.Enabled || c.MFA.Enabled || c.OAuth2.Enabled || !c.PasswordAuth.Enabled || len(c.PasswordAuth.IdentityFields) != 1 || c.PasswordAuth.IdentityFields[0] != "email" {
		return nil, operatorFailure("explicit_password_only_initialization_required")
	}
	count, err := s.app.CountRecords(c)
	if err != nil || count != 1 {
		return nil, operatorFailure("one_explicitly_initialized_native_superuser")
	}
	return s.app, nil
}
