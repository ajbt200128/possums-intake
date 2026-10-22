package ledger

import "testing"

type nonComparableCallbackFailure []byte

func (nonComparableCallbackFailure) Error() string {
	panic("SYNTHETIC_HOSTILE_ERROR_FORMAT")
}

func TestNonComparableCallbackFailurePreservesObservedRollback(t *testing.T) {
	service, _, _ := testService(t)
	err := service.run(background, func(tx *transaction) error {
		if err := tx.exec("INSERT INTO accounts(id,balance,enabled) VALUES('noncomp00000000','0',1)", nil); err != nil {
			return err
		}
		return nonComparableCallbackFailure{1}
	}, true)
	codeIs(t, err, CodeStorage)
	if Failure(err).Outcome != "no_effect" || !service.Ready() {
		t.Fatal("observed rollback was misclassified as uncertain completion")
	}
	_, err = service.Balance(background, "noncomp00000000")
	codeIs(t, err, CodeAccount)
}
