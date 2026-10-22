package ledger

import (
	"context"

	"github.com/pocketbase/dbx"
)

// PurgeSyntheticPayments removes ONE bounded batch. The admission owner must
// repeat while More, and keep payment admission closed on failure. This creates
// no schema or background work and does not establish secure/hard-time erasure.
func (s *Service) PurgeSyntheticPayments(ctx context.Context) (PaymentPurgeResult, error) {
	var result PaymentPurgeResult
	err := s.run(ctx, func(tx *transaction) error {
		if err := tx.requirePayments(); err != nil {
			return err
		}
		var err error
		result.Pruned, err = tx.prunePayments(CleanupLimit)
		if err != nil {
			return err
		}
		var due int
		if err := tx.query("SELECT EXISTS(SELECT 1 FROM payment_purchases WHERE unlink_at<={:now}) OR EXISTS(SELECT 1 FROM payment_sales WHERE sale_date<={:cutoff})", dbx.Params{"now": tx.now, "cutoff": paymentSaleCutoff(tx.now)}).Row(&due); err != nil {
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
	present, err := validatePaymentDDL(tx.query)
	if err != nil || !present {
		return 0, err
	}
	var refs []struct {
		Ref string `db:"purchase_ref"`
	}
	if err := tx.query("SELECT purchase_ref FROM payment_purchases WHERE unlink_at<={:now} ORDER BY unlink_at,purchase_ref LIMIT {:limit}", dbx.Params{"now": tx.now, "limit": limit}).All(&refs); err != nil {
		return 0, err
	}
	pruned := 0
	for _, row := range refs {
		if _, err := tx.payment("purchase_ref", row.Ref); err != nil {
			return 0, err
		}
		if err := tx.exec("DELETE FROM payment_purchases WHERE purchase_ref={:ref}", dbx.Params{"ref": row.Ref}); err != nil {
			return 0, err
		}
		pruned++
	}
	var sales []paymentSale
	if err := tx.query("SELECT "+paymentSaleColumns+" FROM payment_sales WHERE sale_date<={:cutoff} ORDER BY sale_date,sale_id LIMIT {:limit}", dbx.Params{"cutoff": paymentSaleCutoff(tx.now), "limit": limit - pruned}).All(&sales); err != nil {
		return 0, err
	}
	for _, sale := range sales {
		if err := validateSale(sale); err != nil {
			return 0, err
		}
		if err := tx.exec("DELETE FROM payment_sales WHERE sale_id={:id}", dbx.Params{"id": sale.ID}); err != nil {
			return 0, err
		}
		pruned++
	}
	return pruned, nil
}
