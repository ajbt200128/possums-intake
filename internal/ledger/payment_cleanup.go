package ledger

import (
	"context"

	"github.com/pocketbase/dbx"
)

// PurgeSyntheticPayments removes ONE bounded v1 batch. The admission owner must
// repeat while More, and keep payment admission closed on failure. This creates
// no schema or background work and does not establish secure/hard-time erasure.
func (s *Service) PurgeSyntheticPayments(ctx context.Context) (PaymentPurgeResult, error) {
	return s.purgePayments(ctx, PaymentIdentitySynthetic)
}

// PurgeLiveStripePayments drains one bounded v2 batch at the original shared
// cutoff. The caller must complete all batches before opening payment admission.
func (s *Service) PurgeLiveStripePayments(ctx context.Context) (PaymentPurgeResult, error) {
	return s.purgePayments(ctx, PaymentIdentityLiveStripeTax)
}

func (s *Service) purgePayments(ctx context.Context, expected PaymentIdentity) (PaymentPurgeResult, error) {
	var result PaymentPurgeResult
	err := s.run(ctx, func(tx *transaction) error {
		identity, err := tx.paymentIdentity()
		if err != nil {
			return err
		}
		if identity != expected {
			return diagnostic(CodePaymentConflict)
		}
		result.Pruned, err = tx.prunePayments(CleanupLimit)
		if err != nil {
			return err
		}
		var due int
		query := "SELECT EXISTS(SELECT 1 FROM payment_purchases WHERE unlink_at<={:now})"
		if identity == PaymentIdentitySynthetic {
			query += " OR EXISTS(SELECT 1 FROM payment_sales WHERE sale_date<={:cutoff})"
		}
		if err := tx.query(query, dbx.Params{"now": tx.now, "cutoff": paymentSaleCutoff(tx.now)}).Row(&due); err != nil {
			return err
		}
		result.More = due != 0
		return nil
	}, true)
	if err != nil {
		return PaymentPurgeResult{}, err
	}
	return result, nil
}

func (tx *transaction) prunePayments(limit int) (int, error) {
	// Cleanup reuses its existing combined deletion budget. Completely absent
	// schema is legacy-compatible; partial schema is never silently ignored.
	identity, err := validatedPaymentIdentity(tx.query)
	if err != nil || identity == "" {
		return 0, err
	}
	if limit <= 0 {
		return 0, nil
	}
	var refs []struct {
		Ref string `db:"purchase_ref"`
	}
	query := "SELECT purchase_ref FROM payment_purchases WHERE unlink_at<={:now}"
	if identity == PaymentIdentityLiveStripeTax && limit == 1 {
		// A pair cannot fit, but a later unfunded row can. Do not let a funded
		// head row prevent progress with a one-slot residual accounting budget.
		query += " AND sale_id IS NULL"
	}
	query += " ORDER BY unlink_at,purchase_ref LIMIT {:limit}"
	if err := tx.query(query, dbx.Params{"now": tx.now, "limit": limit}).All(&refs); err != nil {
		return 0, err
	}
	pruned := 0
	for _, row := range refs {
		p, err := tx.paymentForIdentity(identity, "purchase_ref", row.Ref)
		if err != nil {
			return 0, err
		}
		paired := identity == PaymentIdentityLiveStripeTax && p.SaleID != nil
		cost := 1
		if paired {
			cost = 2
		}
		// Reserve both slots before either mutation. FK order is purchase then
		// sale, inside the same supported transaction; any failure rolls back.
		if cost > limit-pruned {
			continue
		}
		if err := tx.exec("DELETE FROM payment_purchases WHERE purchase_ref={:ref}", dbx.Params{"ref": row.Ref}); err != nil {
			return 0, err
		}
		if paired {
			if err := tx.exec("DELETE FROM payment_sales WHERE sale_id={:id}", dbx.Params{"id": *p.SaleID}); err != nil {
				return 0, err
			}
		}
		pruned += cost
	}
	if identity == PaymentIdentityLiveStripeTax {
		return pruned, nil
	}
	var sales []paymentSale
	if err := tx.query("SELECT "+paymentSaleColumns+" FROM payment_sales WHERE sale_date<={:cutoff} ORDER BY sale_date,sale_id LIMIT {:limit}", dbx.Params{"cutoff": paymentSaleCutoff(tx.now), "limit": limit - pruned}).All(&sales); err != nil {
		return 0, err
	}
	for _, sale := range sales {
		if err := validateSale(sale, identity); err != nil {
			return 0, err
		}
		if err := tx.exec("DELETE FROM payment_sales WHERE sale_id={:id}", dbx.Params{"id": sale.ID}); err != nil {
			return 0, err
		}
		pruned++
	}
	return pruned, nil
}
