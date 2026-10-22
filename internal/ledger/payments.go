package ledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/pocketbase/dbx"
)

func paymentRandom() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", diagnostic(CodePaymentRandom)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (s *Service) PreparePayment(ctx context.Context, in PreparePaymentInput) (PaymentPurchase, error) {
	terms, ok := PaymentDenomination(in.Denomination)
	if !ok || !validAccount(in.AccountID) || !paymentTokenPattern.MatchString(in.Form.Attempt) || in.Form.IssuedAt <= 0 || in.Form.ExpiresAt <= in.Form.IssuedAt || int64(in.Form.ExpiresAt) > paymentMaxTime || int64(in.Form.ExpiresAt)-int64(in.Form.IssuedAt) > paymentFormMS {
		return PaymentPurchase{}, diagnostic(CodeInput)
	}
	var result PaymentPurchase
	err := s.run(ctx, func(tx *transaction) error {
		if err := tx.requirePayments(); err != nil {
			return err
		}
		// Even an existing attempt cannot authorize a stale form; stored windows
		// cannot be extended by supplying a fresh expiry for the same attempt.
		if int64(in.Form.IssuedAt) > tx.now || tx.now >= int64(in.Form.ExpiresAt) {
			return diagnostic(CodePaymentExpired)
		}
		p, err := tx.payment("form_attempt", in.Form.Attempt)
		if err == nil {
			if p.AccountID != in.AccountID || (PaymentTerms{p.Currency, p.GrossMinor, p.CreditUnits}) != terms || p.FormIssuedAt != in.Form.IssuedAt || p.FormExpiresAt != in.Form.ExpiresAt {
				return diagnostic(CodePaymentConflict)
			}
			if tx.now >= int64(p.UnlinkAt) {
				return diagnostic(CodePaymentExpired)
			}
			result = p
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if tx.now > paymentMaxTime-paymentLinkMS {
			return diagnostic(CodeCorrupt)
		}
		a, err := tx.account(in.AccountID)
		if err != nil {
			return err
		}
		if _, _, err := tx.active(a, false); err != nil {
			return err
		}
		ref, err := paymentRandom()
		if err != nil {
			return err
		}
		create, err := paymentRandom()
		if err != nil {
			return err
		}
		var collision int
		if err := tx.query("SELECT count(*) FROM payment_purchases WHERE purchase_ref={:ref} OR create_key={:key}", dbx.Params{"ref": ref, "key": create}).Row(&collision); err != nil {
			return err
		}
		if collision != 0 || ref == create || ref == in.Form.Attempt || create == in.Form.Attempt {
			return diagnostic(CodePaymentRandom)
		}
		if err := tx.exec("INSERT INTO payment_purchases(purchase_ref,form_attempt,account_id,currency,charge_minor,credit_units,issued_ms,unlink_at,checkout_expires_at,form_issued_ms,form_expires_at,create_key,create_state,funding_state) VALUES({:ref},{:form},{:account},'usd',{:gross},{:units},{:issued},{:unlink},{:expires},{:formissued},{:formexpires},{:key},'prepared','unfunded')", dbx.Params{"ref": ref, "form": in.Form.Attempt, "account": in.AccountID, "gross": string(terms.GrossMinor), "units": string(terms.CreditUnits), "issued": tx.now, "unlink": tx.now + paymentLinkMS, "expires": (tx.now + paymentCheckoutMS) / 1000 * 1000, "formissued": int64(in.Form.IssuedAt), "formexpires": int64(in.Form.ExpiresAt), "key": create}); err != nil {
			return err
		}
		result, err = tx.payment("purchase_ref", ref)
		return err
	}, true)
	if err != nil {
		return PaymentPurchase{}, err
	}
	return result, nil
}

// ClaimPaymentCreate durably grants dispatch at most once. A false claim MUST
// NOT send/replay Checkout creation, even after crash or provider key expiry.
func (s *Service) ClaimPaymentCreate(ctx context.Context, account, ref string) (PaymentPurchase, bool, error) {
	if !validAccount(account) || !paymentTokenPattern.MatchString(ref) {
		return PaymentPurchase{}, false, diagnostic(CodeInput)
	}
	var result PaymentPurchase
	claimed := false
	err := s.run(ctx, func(tx *transaction) error {
		p, err := tx.livePayment(ref, account)
		if err != nil {
			return err
		}
		if p.CreateState != "prepared" {
			result = p
			return nil
		}
		if tx.now >= int64(p.IssuedAt)+paymentCreateMS {
			return diagnostic(CodePaymentExpired)
		}
		if err := tx.exec("UPDATE payment_purchases SET create_state='attempted' WHERE purchase_ref={:ref}", dbx.Params{"ref": ref}); err != nil {
			return err
		}
		p.CreateState = "attempted"
		result, claimed = p, true
		return nil
	}, true)
	if err != nil {
		return PaymentPurchase{}, false, err
	}
	return result, claimed, nil
}

// MarkPaymentCreateUnknown records uncertainty only; never grants another send.
func (s *Service) MarkPaymentCreateUnknown(ctx context.Context, account, ref string) error {
	if !validAccount(account) || !paymentTokenPattern.MatchString(ref) {
		return diagnostic(CodeInput)
	}
	return s.run(ctx, func(tx *transaction) error {
		p, err := tx.livePayment(ref, account)
		if err != nil {
			return err
		}
		if p.CreateState == "prepared" {
			return diagnostic(CodePaymentConflict)
		}
		if p.CreateState != "attempted" {
			return nil
		}
		return tx.exec("UPDATE payment_purchases SET create_state='unknown' WHERE purchase_ref={:ref}", dbx.Params{"ref": ref})
	}, true)
}

func (s *Service) GetPayment(ctx context.Context, account, ref string) (PaymentPurchase, error) {
	if !validAccount(account) {
		return PaymentPurchase{}, diagnostic(CodeInput)
	}
	return s.getPayment(ctx, account, ref)
}

// ReadPaymentForProvider is for a trusted canonical-provider adapter ONLY, never
// a public/customer lookup. Unknown references cannot create purchase authority.
func (s *Service) ReadPaymentForProvider(ctx context.Context, ref string) (PaymentPurchase, error) {
	return s.getPayment(ctx, "", ref)
}
func (s *Service) getPayment(ctx context.Context, account, ref string) (PaymentPurchase, error) {
	if !paymentTokenPattern.MatchString(ref) {
		return PaymentPurchase{}, diagnostic(CodeInput)
	}
	var result PaymentPurchase
	err := s.run(ctx, func(tx *transaction) error {
		var err error
		result, err = tx.livePayment(ref, account)
		return err
	}, true)
	if err != nil {
		return PaymentPurchase{}, err
	}
	return result, nil
}
func (tx *transaction) livePayment(ref, account string) (PaymentPurchase, error) {
	if err := tx.requirePayments(); err != nil {
		return PaymentPurchase{}, err
	}
	p, err := tx.payment("purchase_ref", ref)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentPurchase{}, diagnostic(CodePaymentMissing)
	}
	if err != nil {
		return PaymentPurchase{}, err
	}
	if account != "" && p.AccountID != account {
		return PaymentPurchase{}, diagnostic(CodePaymentMissing)
	}
	if tx.now >= int64(p.UnlinkAt) {
		return PaymentPurchase{}, diagnostic(CodePaymentExpired)
	}
	return p, nil
}

func (s *Service) BindPaymentSession(ctx context.Context, evidence StripeSessionEvidence) (PaymentPurchase, error) {
	if !paymentTokenPattern.MatchString(evidence.Reference) {
		return PaymentPurchase{}, diagnostic(CodeInput)
	}
	var result PaymentPurchase
	err := s.run(ctx, func(tx *transaction) error {
		p, err := tx.livePayment(evidence.Reference, "")
		if err != nil {
			return err
		}
		if err := tx.bindPayment(&p, evidence); err != nil {
			return err
		}
		result = p
		return nil
	}, true)
	if err != nil {
		return PaymentPurchase{}, err
	}
	return result, nil
}
func (tx *transaction) bindPayment(p *PaymentPurchase, e StripeSessionEvidence) error {
	created, ok := paymentSeconds(e.CreatedSeconds)
	expires, expiresOK := paymentSeconds(e.ExpiresSeconds)
	if !ok || !expiresOK || e.Reference != p.Reference || e.LiveMode || e.Mode != "payment" || e.Currency != p.Currency || e.GrossMinor != p.GrossMinor || e.Quantity != 1 || !paymentSessionPattern.MatchString(e.SessionID) || (e.PaymentIntentID != "" && !paymentIntentPattern.MatchString(e.PaymentIntentID)) || p.CreateState == "prepared" || created < int64(p.IssuedAt)-FutureSkewMS || created > int64(p.IssuedAt)+paymentCreateMS+FutureSkewMS || expires != int64(p.CheckoutExpiresAt) || created > tx.now+FutureSkewMS {
		return diagnostic(CodePaymentConflict)
	}
	if tx.now >= int64(p.UnlinkAt) || tx.now >= created+paymentLinkMS {
		return diagnostic(CodePaymentExpired)
	}
	if p.SessionID != nil && (*p.SessionID != e.SessionID || p.SessionCreatedAt == nil || int64(*p.SessionCreatedAt) != created) {
		return diagnostic(CodePaymentConflict)
	}
	if p.PaymentIntentID != nil && e.PaymentIntentID != "" && *p.PaymentIntentID != e.PaymentIntentID {
		return diagnostic(CodePaymentConflict)
	}
	var conflicts int
	if err := tx.query("SELECT count(*) FROM payment_purchases WHERE purchase_ref!={:ref} AND (stripe_session_id={:session} OR stripe_payment_intent_id={:intent})", dbx.Params{"ref": p.Reference, "session": e.SessionID, "intent": e.PaymentIntentID}).Row(&conflicts); err != nil {
		return err
	}
	if conflicts != 0 {
		return diagnostic(CodePaymentConflict)
	}
	p.SessionID = &e.SessionID
	at := Millis(created)
	p.SessionCreatedAt = &at
	if e.PaymentIntentID != "" {
		p.PaymentIntentID = &e.PaymentIntentID
	}
	var intent any
	if p.PaymentIntentID != nil {
		intent = *p.PaymentIntentID
	}
	if err := tx.exec("UPDATE payment_purchases SET create_state='known',stripe_session_id={:session},stripe_payment_intent_id={:intent},session_created_ms={:created} WHERE purchase_ref={:ref}", dbx.Params{"ref": p.Reference, "session": e.SessionID, "intent": intent, "created": created}); err != nil {
		return err
	}
	p.CreateState = "known"
	return nil
}

// FundStripePurchase accepts canonical typed evidence, never provider objects or
// events. Every accounting effect uses this transaction's app. No remote work,
// refund, generic ledger credit marker or availability receipt is involved.
func (s *Service) FundStripePurchase(ctx context.Context, e StripeFundingEvidence) (PaymentFundingResult, error) {
	if !paymentTokenPattern.MatchString(e.Session.Reference) {
		return PaymentFundingResult{}, diagnostic(CodeInput)
	}
	if !paymentIntentPattern.MatchString(e.Session.PaymentIntentID) {
		return PaymentFundingResult{}, diagnostic(CodePaymentConflict)
	}
	var result PaymentFundingResult
	err := s.run(ctx, func(tx *transaction) error {
		p, err := tx.livePayment(e.Session.Reference, "")
		if err != nil {
			return err
		}
		// Refresh after the local read, not at the adapter's earlier network time.
		if err := tx.clock(); err != nil {
			return err
		}
		if err := tx.bindPayment(&p, e.Session); err != nil {
			return err
		}
		if p.FundingState == "funded" {
			result.Status = PaymentDuplicate
			return nil
		}
		if p.FundingState == "blocked" {
			result.Status = PaymentBlocked
			return nil
		}
		refunded, refundErr := e.AmountRefunded.units()
		if refundErr != nil {
			return diagnostic(CodePaymentConflict)
		}
		if e.RefundObserved || e.Refunded || refunded != 0 || e.Disputed {
			if err := tx.exec("UPDATE payment_purchases SET funding_state='blocked' WHERE purchase_ref={:ref}", dbx.Params{"ref": p.Reference}); err != nil {
				return err
			}
			result.Status = PaymentBlocked
			return nil
		}
		chargeAt, validTime := paymentSeconds(e.ChargeCreatedSeconds)
		if e.PaymentStatus != "paid" || e.IntentStatus != "succeeded" || e.IntentAmount != p.GrossMinor || e.AmountReceived != p.GrossMinor || e.ChargeStatus != "succeeded" || !e.Paid || !e.Captured || e.ChargeCurrency != p.Currency || e.ChargeAmount != p.GrossMinor || e.AmountCaptured != p.GrossMinor || !validTime || chargeAt < int64(*p.SessionCreatedAt) || chargeAt > int64(p.CheckoutExpiresAt) || chargeAt > tx.now+FutureSkewMS {
			return diagnostic(CodePaymentConflict)
		}
		a, err := tx.account(p.AccountID)
		if err != nil {
			return err
		}
		if _, _, err := tx.active(a, true); err != nil {
			return err
		}
		total, _ := a.Balance.units()
		units, _ := p.CreditUnits.units()
		after, err := add(total, units)
		if err != nil {
			return err
		}
		sale, err := paymentRandom()
		if err != nil {
			return err
		}
		var collision int
		if err := tx.query("SELECT count(*) FROM payment_sales WHERE sale_id={:id}", dbx.Params{"id": sale}).Row(&collision); err != nil {
			return err
		}
		if collision != 0 || sale == p.Reference || sale == p.CreateKey || sale == p.FormAttempt {
			return diagnostic(CodePaymentRandom)
		}
		if err := tx.exec("UPDATE accounts SET balance={:after} WHERE id={:account}", dbx.Params{"after": string(money(after)), "account": p.AccountID}); err != nil {
			return err
		}
		if err := tx.exec("INSERT INTO payment_sales(sale_id,account_id,sale_date,currency,gross_minor,credit_units) VALUES({:id},{:account},{:date},'usd',{:gross},{:units})", dbx.Params{"id": sale, "account": p.AccountID, "date": paymentSaleDate(chargeAt), "gross": string(p.GrossMinor), "units": string(p.CreditUnits)}); err != nil {
			return err
		}
		if err := tx.exec("UPDATE payment_purchases SET funding_state='funded',sale_id={:sale} WHERE purchase_ref={:ref}", dbx.Params{"sale": sale, "ref": p.Reference}); err != nil {
			return err
		}
		result.Status = PaymentFunded
		return nil
	}, true)
	if err != nil {
		return PaymentFundingResult{}, err
	}
	return result, nil
}
