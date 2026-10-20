package ledger

import "testing"

func TestCustomerAuthAppPreservesAccountingGuard(t *testing.T) {
	s, _, _ := testService(t)
	id := nativeCreate(t, s, "customer-app-synthetic", "20")
	app, err := s.CustomerAuthApp()
	if err != nil || app != s.app {
		t.Fatal("SITE_TEST_SAME_APP")
	}
	record, err := app.FindRecordById(AccountCollectionID, id)
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_READ")
	}
	record.Set("account_id", "different-synthetic")
	if app.Save(record) == nil {
		t.Fatal("SITE_TEST_ACCOUNT_GUARD_REMOVED")
	}
	if s.Close() != nil {
		t.Fatal("SITE_TEST_SERVICE_CLOSE")
	}
	app, err = s.CustomerAuthApp()
	if app != nil {
		t.Fatal("SITE_TEST_CLOSED_APP_EXPOSED")
	}
	codeIs(t, err, CodeClosed)
}
