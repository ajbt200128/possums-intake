package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

type websiteSeedBrokenApp struct {
	core.App
	failure error
	panics  bool
}

func (a websiteSeedBrokenApp) RunInTransaction(func(core.App) error) error {
	if a.panics {
		panic("SYNTHETIC_SEED_PRIVATE_SENTINEL")
	}
	return a.failure
}
func TestWebsiteLocalSDKSeedFailurePrivacyAndTypes(t *testing.T) {
	for _, test := range []struct {
		app  websiteSeedBrokenApp
		kind failuretype.Type
	}{
		{websiteSeedBrokenApp{failure: errors.New("SYNTHETIC_SEED_PRIVATE_SENTINEL")}, failuretype.NativeWriteUnknown},
		{websiteSeedBrokenApp{panics: true}, failuretype.NativeWriteUnknown},
		{websiteSeedBrokenApp{failure: context.Canceled}, failuretype.Cancelled},
		{websiteSeedBrokenApp{failure: context.DeadlineExceeded}, failuretype.DeadlineExceeded},
	} {
		err := seedWebsiteCustomer(context.Background(), test.app)
		if err == nil {
			t.Fatal("SITE_TEST_SEED_FAULT_ACCEPTED")
		}
		d := ledger.Failure(err)
		if d.Kind() != test.kind || d.Stage != "site_local_customer_fixture" || d.Constraint == "" || d.NextStep == "" {
			t.Fatal("SITE_TEST_SEED_TYPED_BOUNDARY")
		}
		b, e := json.Marshal(d)
		if e != nil || strings.Contains(string(b), "SYNTHETIC_SEED_PRIVATE_SENTINEL") {
			t.Fatal("SITE_TEST_SEED_EXCEPTION_DISCLOSURE")
		}
	}
}
