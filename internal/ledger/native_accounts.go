package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"possums-intake/internal/failuretype"
)

var nativeIDPattern = regexp.MustCompile(`^[a-z0-9]{15}$`)

// NativeAccountPatch contains only typed financial values. Credentials are
// created only through supported native auth records, never accounting calls.
type NativeAccountPatch struct {
	Balance *Money
	Enabled *bool
	ID      *string
}

func nativeFailure(code Code, constraint string) *Diagnostic {
	d := diagnostic(code)
	d.Stage = "native_account_write"
	d.Constraint = constraint
	d.NextStep = "Refresh stored state privately. Native edits are synthetic only, not credit receipts. Do not automatically repeat an uncertain absolute edit."
	return d
}

const accountValidationHook = "intakeAccountValidation"

func bindAccountValidation(app core.App) {
	app.OnRecordValidate(AccountCollectionName).Bind(&hook.Handler[*core.RecordEvent]{Id: accountValidationHook, Func: func(e *core.RecordEvent) error {
		r := e.Record
		if !nativeIDPattern.MatchString(r.Id) {
			return nativeFailure(CodeInput, "valid_account_record_identity")
		}
		if r.IsNew() && r.GetString("balance") == "" {
			r.Set("balance", "0")
		}
		if !r.IsNew() && r.Id != r.Original().Id {
			return nativeFailure(CodeInput, "immutable_account_identity")
		}
		if _, err := ParseMoney(r.GetString("balance")); err != nil {
			return nativeFailure(CodeInput, "canonical_uint64_text_balance")
		}
		return e.Next()
	}})
}

// encodeNative runs before commit. No success bytes escape until run observes
// successful completion; delivery errors cannot undo that known completion.
func encodeNative(r *core.Record) ([]byte, error) {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 4096 {
		if err == nil {
			err = &Diagnostic{}
		}
		return nil, CanonicalFailure(err, failuretype.ResponseEncodingUnknown)
	}
	return raw, nil
}

func (s *Service) NativeAccountUpdate(ctx context.Context, id string, in NativeAccountPatch) ([]byte, error) {
	if !nativeIDPattern.MatchString(id) {
		return nil, diagnostic(CodeInput)
	}
	if in.Balance != nil {
		if _, err := in.Balance.units(); err != nil {
			return nil, diagnostic(CodeInput)
		}
	}
	var result []byte
	err := s.run(ctx, func(tx *transaction) error {
		// Ignore stock controller's eager-loaded record. Decisions and save use the
		// same fresh transaction-scoped record and accounting expiry/watermark.
		r, err := tx.app.FindRecordById(AccountCollectionID, id, func(q *dbx.SelectQuery) error {
			q.WithContext(tx.ctx)
			return nil
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nativeFailure(CodeAccount, "existing_native_account")
		}
		if err != nil {
			return err
		}
		a, err := tx.account(r.Id)
		if err != nil {
			return err
		}
		if in.ID != nil && *in.ID != r.Id {
			return nativeFailure(CodeInput, "immutable_account_identity")
		}
		pending, _, err := tx.active(a, true)
		if err != nil {
			return err
		}
		if in.Balance != nil && *in.Balance != a.Balance {
			if len(pending) != 0 {
				return nativeFailure(CodeNativeHold, "no_active_holds_for_changed_balance")
			}
			r.Set("balance", string(*in.Balance))
		}
		if in.Enabled != nil {
			r.Set("enabled", *in.Enabled)
		}
		if err := tx.app.SaveWithContext(tx.ctx, r); err != nil {
			return err
		}
		result, err = encodeNative(r)
		return err
	}, true)
	if err != nil {
		return nil, err
	}
	return result, nil
}
