package ledger

import (
	"context"
	"possums-intake/internal/testfixture"
)

// Explicit synthetic record creation, not the retired label provisioning API.
func fixtureAccount(s *Service, ctx context.Context, id string) (Account, error) {
	var a Account
	err := s.run(ctx, func(tx *transaction) error {
		r, err := testfixture.CreateRecord(tx.ctx, tx.app, id, "0", true)
		if err != nil {
			return err
		}
		a = Account{AccountID: r.Id, Balance: "0", Enabled: true}
		return nil
	}, true)
	return a, err
}
