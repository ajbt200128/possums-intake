package ledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"possums-intake/internal/failuretype"
)

var nativeIDPattern = regexp.MustCompile(`^[a-z0-9]{15}$`)

// NativeAccountCreate and NativeAccountPatch contain only typed, validated
// editor values. Absolute synthetic edits are not funding or credit receipts.
type NativeAccountCreate struct {
	AccountID string
	Balance   Money
	Enabled   bool
}
type NativeAccountPatch struct {
	AccountID *string
	Balance   *Money
	Enabled   *bool
	ID        *string
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
		if !validAccount(r.GetString("account_id")) || !nativeIDPattern.MatchString(r.Id) {
			return nativeFailure(CodeInput, "valid_external_and_internal_identity")
		}
		if _, err := ParseMoney(r.GetString("balance")); err != nil {
			return nativeFailure(CodeInput, "canonical_uint64_text_balance")
		}
		if !r.IsNew() && (r.Id != r.Original().Id || r.GetString("account_id") != r.Original().GetString("account_id")) {
			return nativeFailure(CodeInput, "immutable_account_identity")
		}
		return e.Next()
	}})
}

func (tx *transaction) createAccount(in NativeAccountCreate) (*core.Record, error) {
	c, err := tx.app.FindCollectionByNameOrId(AccountCollectionID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		var bytes [8]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil, nativeFailure(CodeRandom, "unique_internal_account_id")
		}
		id := hex.EncodeToString(bytes[:])[:15]
		var count int
		if err := tx.query("SELECT count(*) FROM ledger_accounts WHERE id={:id}", map[string]any{"id": id}).Row(&count); err != nil {
			return nil, err
		}
		if count != 0 {
			continue
		}
		r := core.NewRecord(c)
		r.Id = id
		r.Set("account_id", in.AccountID)
		r.Set("balance", string(in.Balance))
		r.Set("enabled", in.Enabled)
		if err := tx.app.SaveWithContext(tx.ctx, r); err != nil {
			return nil, err
		}
		return r, nil
	}
	return nil, nativeFailure(CodeRandom, "unique_internal_account_id")
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

func (s *Service) NativeAccountCreate(ctx context.Context, in NativeAccountCreate) ([]byte, error) {
	if !validAccount(in.AccountID) {
		return nil, diagnostic(CodeInput)
	}
	if _, err := in.Balance.units(); err != nil {
		return nil, diagnostic(CodeInput)
	}
	var result []byte
	err := s.run(ctx, func(tx *transaction) error {
		var count int
		if err := tx.query("SELECT count(*) FROM ledger_accounts WHERE account_id={:id}", map[string]any{"id": in.AccountID}).Row(&count); err != nil {
			return err
		}
		if count != 0 {
			return nativeFailure(CodeInput, "unique_external_account_id")
		}
		r, err := tx.createAccount(in)
		if err != nil {
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
		a, err := tx.account(r.GetString("account_id"))
		if err != nil {
			return err
		}
		if in.ID != nil && *in.ID != r.Id || in.AccountID != nil && *in.AccountID != a.AccountID {
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
