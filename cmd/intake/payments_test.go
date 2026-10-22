package main

import (
 "possums-intake/internal/testfixture"
	"context"
	"path/filepath"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

func TestSyntheticPaymentInitCLIExplicitAndAdditive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	out, stderr, success := runCLI(t, "synthetic-payments-init", "--state", dir)
	checkFailure(t, out, stderr, success, ledger.CodeMissing)
	out, stderr, success = runCLI(t, "init", "--state", dir)
	if !success || stderr != "" {
		t.Fatal("PAYMENT_TEST_BASE_INIT")
	}
	ctx := context.Background()
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("PAYMENT_TEST_OPEN")
	}
	if _, err := testfixture.Create(ctx, service, "paymentaccount0"); err != nil {
		t.Fatal("PAYMENT_TEST_PROVISION")
	}
	if service.Close() != nil {
		t.Fatal("PAYMENT_TEST_CLOSE")
	}
	for range 2 {
		out, stderr, success = runCLI(t, "synthetic-payments-init", "--state", dir)
		if !success || stderr != "" || out != "{\"status\":\"synthetic_payments_initialized\"}\n" {
			t.Fatal("PAYMENT_TEST_EXPLICIT_SETUP")
		}
	}
	service, err = ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("PAYMENT_TEST_REOPEN")
	}
	defer service.Close()
	balance, err := service.Balance(ctx, "paymentaccount0")
	if err != nil || balance.AccountBalance.Balance != "0" {
		t.Fatal("PAYMENT_TEST_PRESERVED_ACCOUNT")
	}
	now := time.Now().UnixMilli()
	_, err = service.PreparePayment(ctx, ledger.PreparePaymentInput{AccountID: "paymentaccount0", Denomination: "usd5", Form: ledger.PaymentForm{Attempt: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IssuedAt: ledger.Millis(now), ExpiresAt: ledger.Millis(now + 600000)}})
	if err != nil {
		t.Fatal("PAYMENT_TEST_FEATURE_PRESENT")
	}
}

func TestSyntheticPaymentInitCLIRejectsRuntimeOptions(t *testing.T) {
	for _, args := range [][]string{{}, {"--state", "relative"}, {"--state", "/synthetic", "--listen", "127.0.0.1:0"}, {"--state", "/synthetic", "--tinfoil-volume", "ledger"}, {"--state", "/synthetic", "--operator"}} {
		out, stderr, success := runCLI(t, append([]string{"synthetic-payments-init"}, args...)...)
		checkFailure(t, out, stderr, success, "PAYMENT_SETUP_OPTIONS_INVALID")
	}
}
