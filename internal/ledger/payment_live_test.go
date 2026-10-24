package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
)

// Public-shape synthetic fixture only, not a selected product classification.
const fixtureTaxCode = "txcd_12345678"

func livePaymentService(t *testing.T) (*Service, string, int64) {
	t.Helper()
	s, dir, now := testService(t)
	// Align to provider seconds while retaining a time ahead of SQLite wall time.
	now = (now/1000 + 1) * 1000
	advance(t, s, now)
	if err := s.InitializeLiveStripePayments(background); err != nil {
		t.Fatalf("live setup: %s", Failure(err).Code)
	}
	a, err := fixtureAccount(s, background, "paymentaccount0")
	must(t, a, err)
	return s, dir, now
}
func prepareLivePurchase(t *testing.T, s *Service, now int64, n int) PaymentPurchase {
	t.Helper()
	p, err := s.PreparePayment(background, PreparePaymentInput{AccountID: "paymentaccount0", Denomination: "usd5", Form: paymentForm(now, n), TaxCode: fixtureTaxCode})
	return must(t, p, err)
}
func liveFundingEvidence(p PaymentPurchase, n int, tax Money) StripeFundingEvidence {
	e := fundingEvidence(p, n)
	e.Session.LiveMode = true
	e.Session.TaxCode = p.TaxCode
	principal, _ := p.GrossMinor.units()
	taxUnits, _ := tax.units()
	total, _ := add(principal, taxUnits)
	e.Tax = &StripeTaxEvidence{Amount: tax, Total: money(total), Complete: true, Exclusive: true, LiabilitySelf: true}
	e.IntentAmount, e.AmountReceived, e.ChargeAmount, e.AmountCaptured = money(total), money(total), money(total), money(total)
	return e
}
func readLiveSale(t *testing.T, s *Service) paymentSale {
	t.Helper()
	var sale paymentSale
	if err := s.app.DB().NewQuery("SELECT " + saleColumns(PaymentIdentityLiveStripeTax) + " FROM payment_sales").One(&sale); err != nil {
		t.Fatal("live sale fixture read")
	}
	return sale
}

// Snapshot all existing synthetic database rows in memory without emitting
// their values. This includes native auth/settings and ledger replay/hold state.
func foundationSnapshot(t *testing.T, s *Service) map[string][]string {
	t.Helper()
	var tables []struct {
		Name string `db:"name"`
	}
	if err := s.app.DB().NewQuery("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT GLOB 'payment_*' ORDER BY name").All(&tables); err != nil {
		t.Fatal("foundation table inventory")
	}
	out := map[string][]string{}
	for _, table := range tables {
		rows, err := s.app.DB().NewQuery(`SELECT * FROM "` + strings.ReplaceAll(table.Name, `"`, `""`) + `"`).Rows()
		if err != nil {
			t.Fatal("foundation row snapshot")
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal("foundation columns")
		}
		out[table.Name] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal("foundation scan")
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal("foundation comparison encoding")
			}
			out[table.Name] = append(out[table.Name], string(encoded))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal("foundation iteration")
		}
		rows.Close()
		sort.Strings(out[table.Name])
	}
	return out
}

func TestLivePaymentsAbsentSetupPreservesFoundationAndRestart(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "paymentaccount0", "100", now)
	hold := reserve(t, s, ReserveInput{"paymentaccount0", "40", 600, key(now, 2)})
	before := foundationSnapshot(t, s)
	identity, err := s.PaymentIdentity(background)
	codeIs(t, err, CodePaymentDisabled)
	if identity != "" {
		t.Fatal("absent identity supplied authority")
	}
	for i := 0; i < 2; i++ {
		if err := s.InitializeLiveStripePayments(background); err != nil {
			t.Fatalf("explicit live init: %s", Failure(err).Code)
		}
		identity, err = s.PaymentIdentity(background)
		if must(t, identity, err) != PaymentIdentityLiveStripeTax || !reflect.DeepEqual(before, foundationSnapshot(t, s)) {
			t.Fatal("setup/identity changed existing foundation")
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal("foundation close")
	}
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	if !reflect.DeepEqual(before, foundationSnapshot(t, reopened)) {
		t.Fatal("restart changed foundation")
	}
	if b := balance(t, reopened, "paymentaccount0"); b.Available != "60" || len(b.PendingReservations) != 1 || b.PendingReservations[0].ReservationID != hold.ReservationID {
		t.Fatal("restart lost original accounting")
	}
	credit, err := reopened.Credit(background, CreditInput{"paymentaccount0", "100", key(now, 1)})
	if must(t, credit, err).BalanceAfter != "100" {
		t.Fatal("setup reset original replay evidence")
	}
	var columns []struct {
		Name string `db:"name"`
	}
	if err := reopened.app.DB().NewQuery("SELECT name FROM pragma_table_info('payment_sales') ORDER BY cid").All(&columns); err != nil {
		t.Fatal("live sales inventory")
	}
	var names []string
	for _, c := range columns {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"sale_id", "account_id", "sale_date", "currency", "principal_minor", "tax_minor", "gross_minor", "credit_units"}) {
		t.Fatal("live sales retained unapproved columns")
	}
	for _, table := range []string{"payment_meta", "payment_sales", "payment_purchases"} {
		for _, key := range []string{"rowid", "_rowid_", "oid"} {
			var hidden int
			if err := reopened.app.DB().NewQuery("SELECT " + key + " FROM " + table).Row(&hidden); err == nil {
				t.Fatal("live schema exposed sequential bridge")
			}
		}
	}
}

func TestLivePaymentsRefuseV1WithoutEffect(t *testing.T) {
	s, _, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	r, err := s.FundStripePurchase(background, fundingEvidence(p, 1))
	must(t, r, err)
	before := foundationSnapshot(t, s)
	codeIs(t, s.InitializeLiveStripePayments(background), CodePaymentConflict)
	identity, err := s.PaymentIdentity(background)
	if must(t, identity, err) != PaymentIdentitySynthetic || !reflect.DeepEqual(before, foundationSnapshot(t, s)) {
		t.Fatal("v1 relabeled or accounting changed")
	}
	if purchases, sales, _ := paymentCounts(t, s); purchases != 1 || sales != 1 {
		t.Fatal("v1 setup changed records")
	}
	var days int
	if err := s.app.DB().NewQuery("SELECT sales_retention_days FROM payment_meta").Row(&days); err != nil || days != 90 {
		t.Fatal("v1 retention changed")
	}
	_, err = s.PurgeLiveStripePayments(background)
	codeIs(t, err, CodePaymentConflict)
	if !s.Ready() {
		t.Fatal("mode refusal closed valid v1 state")
	}
}

func TestLivePaymentsTaxCodeModeAndImmutableBinding(t *testing.T) {
	s, _, now := livePaymentService(t)
	for i, taxCode := range []string{"", "txcd_1234567", "txcd_123456789", "txcd_abcdefgh", "txcd_12345678\n"} {
		_, err := s.PreparePayment(background, PreparePaymentInput{AccountID: "paymentaccount0", Denomination: "usd5", Form: paymentForm(now, i+10), TaxCode: taxCode})
		codeIs(t, err, CodeInput)
	}
	p := prepareLivePurchase(t, s, now, 1)
	in := PreparePaymentInput{AccountID: p.AccountID, Denomination: "usd5", Form: paymentForm(now, 1), TaxCode: fixtureTaxCode}
	repeat, err := s.PreparePayment(background, in)
	if !reflect.DeepEqual(must(t, repeat, err), p) {
		t.Fatal("live duplicate changed original purchase")
	}
	in.TaxCode = "txcd_87654321"
	_, err = s.PreparePayment(background, in)
	codeIs(t, err, CodePaymentConflict)
	p = claimPurchase(t, s, p)
	e := liveFundingEvidence(p, 1, "23")
	for _, change := range []func(*StripeSessionEvidence){
		func(e *StripeSessionEvidence) { e.LiveMode = false },
		func(e *StripeSessionEvidence) { e.TaxCode = "txcd_87654321" },
		func(e *StripeSessionEvidence) { e.Mode = "subscription" },
		func(e *StripeSessionEvidence) { e.GrossMinor = "523" },
	} {
		bad := e.Session
		change(&bad)
		_, err := s.BindPaymentSession(background, bad)
		codeIs(t, err, CodePaymentConflict)
	}
	// Open create binding has no final tax evidence; funding later supplies it.
	bound, err := s.BindPaymentSession(background, e.Session)
	if must(t, bound, err).TaxCode != fixtureTaxCode {
		t.Fatal("binding lost immutable original tax code")
	}
	swapped := e.Session
	swapped.TaxCode = "txcd_87654321"
	_, err = s.BindPaymentSession(background, swapped)
	codeIs(t, err, CodePaymentConflict)
	e.Session.SessionID = "cs_rebound"
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentConflict)
	advance(t, s, now+paymentFormMS)
	in.TaxCode = fixtureTaxCode
	_, err = s.PreparePayment(background, in)
	codeIs(t, err, CodePaymentExpired)
	if purchases, sales, _ := paymentCounts(t, s); purchases != 1 || sales != 0 || balance(t, s, p.AccountID).Available != "0" {
		t.Fatal("invalid mode/code/form produced effects")
	}
	_, err = s.PurgeSyntheticPayments(background)
	codeIs(t, err, CodePaymentConflict)
}

func TestSyntheticPaymentsRefuseTaxAndLiveEvidence(t *testing.T) {
	s, _, now := paymentService(t)
	_, err := s.PreparePayment(background, PreparePaymentInput{AccountID: "paymentaccount0", Denomination: "usd5", Form: paymentForm(now, 1), TaxCode: fixtureTaxCode})
	codeIs(t, err, CodeInput)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 2))
	for _, change := range []func(*StripeFundingEvidence){
		func(e *StripeFundingEvidence) {
			e.Tax = &StripeTaxEvidence{Amount: "0", Total: "500", Complete: true, Exclusive: true, LiabilitySelf: true}
		},
		func(e *StripeFundingEvidence) { e.Session.TaxCode = fixtureTaxCode },
		func(e *StripeFundingEvidence) { e.Session.LiveMode = true },
	} {
		e := fundingEvidence(p, 2)
		change(&e)
		_, err := s.FundStripePurchase(background, e)
		codeIs(t, err, CodePaymentConflict)
	}
	if purchases, sales, _ := paymentCounts(t, s); purchases != 1 || sales != 0 {
		t.Fatal("v1 accepted tax/live evidence")
	}
}

func TestLivePaymentsExactTaxAndOnceOnlyFunding(t *testing.T) {
	for _, tax := range []Money{"0", "43"} {
		t.Run(string(tax), func(t *testing.T) {
			s, dir, now := livePaymentService(t)
			p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
			e := liveFundingEvidence(p, 1, tax)
			var wg sync.WaitGroup
			results := make(chan PaymentFundingStatus, 16)
			failures := make(chan error, 16)
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := s.FundStripePurchase(background, e)
					results <- r.Status
					failures <- err
				}()
			}
			wg.Wait()
			close(results)
			close(failures)
			funded, duplicates := 0, 0
			for status := range results {
				if status == PaymentFunded {
					funded++
				} else if status == PaymentDuplicate {
					duplicates++
				}
			}
			for err := range failures {
				if err != nil {
					t.Fatalf("live concurrent funding: %s", Failure(err).Code)
				}
			}
			if funded != 1 || duplicates != 15 || balance(t, s, p.AccountID).Available != "5000000" {
				t.Fatal("tax became credit or funding was not once-only")
			}
			sale := readLiveSale(t, s)
			if sale.PrincipalMinor != "500" || sale.TaxMinor != tax || sale.GrossMinor != e.Tax.Total || sale.CreditUnits != "5000000" || sale.Date != paymentSaleDate(e.ChargeCreatedSeconds*1000) {
				t.Fatal("sale principal/tax/collected total mismatch")
			}
			if purchases, sales, credits := paymentCounts(t, s); purchases != 1 || sales != 1 || credits != 0 {
				t.Fatal("unexpected live payment record")
			}
			// A later partial/refund notification need not carry complete tax or
			// full-charge amounts; original funding remains authoritative.
			e.RefundObserved, e.Disputed, e.Refunded = true, true, true
			e.AmountRefunded = "not canonical"
			e.Tax = nil
			e.IntentAmount, e.AmountCaptured = "0", "0"
			r, err := s.FundStripePurchase(background, e)
			if must(t, r, err).Status != PaymentDuplicate || balance(t, s, p.AccountID).Available != "5000000" {
				t.Fatal("known-funded refund erased receipt or clawed back credit")
			}
			s.Close()
			reopened, err := Open(background, dir)
			must(t, reopened, err)
			defer reopened.Close()
			r, err = reopened.FundStripePurchase(background, e)
			if must(t, r, err).Status != PaymentDuplicate || readLiveSale(t, reopened) != sale {
				t.Fatal("restart changed original live receipt")
			}
		})
	}
}

func TestLivePaymentsRejectIncompleteOrInexactTax(t *testing.T) {
	mutations := map[string]func(*StripeFundingEvidence){
		"missing":            func(e *StripeFundingEvidence) { e.Tax = nil },
		"incomplete":         func(e *StripeFundingEvidence) { e.Tax.Complete = false },
		"inclusive":          func(e *StripeFundingEvidence) { e.Tax.Exclusive = false },
		"not_self":           func(e *StripeFundingEvidence) { e.Tax.LiabilitySelf = false },
		"negative":           func(e *StripeFundingEvidence) { e.Tax.Amount = "-1" },
		"noncanonical":       func(e *StripeFundingEvidence) { e.Tax.Amount = "01" },
		"overflow":           func(e *StripeFundingEvidence) { e.Tax.Amount = "18446744073709551615" },
		"total_overflow":     func(e *StripeFundingEvidence) { e.Tax.Total = "18446744073709551616" },
		"total_zero":         func(e *StripeFundingEvidence) { e.Tax.Total = "0" },
		"mismatch":           func(e *StripeFundingEvidence) { e.Tax.Total = "544" },
		"principal_tax_swap": func(e *StripeFundingEvidence) { e.Session.GrossMinor = "543" },
		"intent":             func(e *StripeFundingEvidence) { e.IntentAmount = "500" },
		"received":           func(e *StripeFundingEvidence) { e.AmountReceived = "500" },
		"charge":             func(e *StripeFundingEvidence) { e.ChargeAmount = "500" },
		"captured":           func(e *StripeFundingEvidence) { e.AmountCaptured = "500" },
		"test_mode":          func(e *StripeFundingEvidence) { e.Session.LiveMode = false },
		"tax_code":           func(e *StripeFundingEvidence) { e.Session.TaxCode = "txcd_87654321" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, _, now := livePaymentService(t)
			p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
			e := liveFundingEvidence(p, 1, "43")
			mutate(&e)
			r, err := s.FundStripePurchase(background, e)
			codeIs(t, err, CodePaymentConflict)
			if r != (PaymentFundingResult{}) || balance(t, s, p.AccountID).Available != "0" {
				t.Fatal("invalid tax supplied funding receipt")
			}
			stored, err := s.GetPayment(background, p.AccountID, p.Reference)
			if must(t, stored, err).CreateState != "attempted" || stored.SessionID != nil || stored.FundingState != "unfunded" {
				t.Fatal("invalid tax partially bound purchase")
			}
			if _, sales, _ := paymentCounts(t, s); sales != 0 {
				t.Fatal("invalid tax persisted sale")
			}
		})
	}
}

func TestLivePaymentsStickyBlockAndCreditOverflow(t *testing.T) {
	for _, reason := range []string{"refund_observed", "refunded", "partial", "disputed", "overflow"} {
		t.Run(reason, func(t *testing.T) {
			s, _, now := livePaymentService(t)
			if reason == "overflow" {
				fund(t, s, "paymentaccount0", "18446744073709551615", now)
			}
			p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
			e := liveFundingEvidence(p, 1, "43")
			switch reason {
			case "refund_observed":
				e.RefundObserved = true
			case "refunded":
				e.Refunded = true
			case "partial":
				e.AmountRefunded = "1"
			case "disputed":
				e.Disputed = true
			}
			r, err := s.FundStripePurchase(background, e)
			if reason == "overflow" {
				codeIs(t, err, CodeOverflow)
				if r != (PaymentFundingResult{}) || balance(t, s, p.AccountID).Available != "18446744073709551615" {
					t.Fatal("credit overflow changed balance")
				}
			} else {
				if must(t, r, err).Status != PaymentBlocked {
					t.Fatal("pre-funding refund/dispute not blocked")
				}
				r, err = s.FundStripePurchase(background, liveFundingEvidence(p, 1, "43"))
				if must(t, r, err).Status != PaymentBlocked || balance(t, s, p.AccountID).Available != "0" {
					t.Fatal("later success removed sticky block")
				}
			}
			if _, sales, _ := paymentCounts(t, s); sales != 0 {
				t.Fatal("blocked/overflow sale survived")
			}
		})
	}
}

func TestLivePaymentsUnknownAndKnownCompletion(t *testing.T) {
	s, _, now := livePaymentService(t)
	p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
	e := liveFundingEvidence(p, 1, "43")
	original := s.app
	s.app = lostCompletionApp{original}
	r, err := s.FundStripePurchase(background, e)
	codeIs(t, err, CodeCommit)
	if r != (PaymentFundingResult{}) {
		t.Fatal("unknown commit supplied receipt")
	}
	s.app = afterCommitFailure{original}
	r, err = s.FundStripePurchase(background, e)
	s.app = original
	if must(t, r, err).Status != PaymentDuplicate || balance(t, s, p.AccountID).Available != "5000000" {
		t.Fatal("completion reconciliation changed credit")
	}
}

func TestLivePaymentsRejectSchemaDriftAndReverseSaleCorruption(t *testing.T) {
	mutations := map[string]struct {
		sql  string
		code Code
	}{
		"orphan":           {"DELETE FROM payment_purchases", CodeCorrupt},
		"missing_sale":     {"DELETE FROM payment_sales", CodeCorrupt},
		"account":          {"UPDATE payment_sales SET account_id='otheraccount000'", CodeCorrupt},
		"currency":         {"UPDATE payment_sales SET currency='eur'", CodeCorrupt},
		"principal":        {"UPDATE payment_sales SET principal_minor='1000',credit_units='10000000',gross_minor='1043'", CodeCorrupt},
		"credit":           {"UPDATE payment_sales SET credit_units='1'", CodeCorrupt},
		"tax":              {"UPDATE payment_sales SET tax_minor='44'", CodeCorrupt},
		"noncanonical_tax": {"UPDATE payment_sales SET tax_minor='043'", CodeCorrupt},
		"total":            {"UPDATE payment_sales SET gross_minor='544'", CodeCorrupt},
		"date":             {"UPDATE payment_sales SET sale_date='2026-02-30'", CodeCorrupt},
		"code":             {"UPDATE payment_purchases SET tax_code=''", CodeCorrupt},
		"metadata":         {"UPDATE payment_meta SET identity='synthetic-only'", CodeSchema},
		"version":          {"UPDATE payment_meta SET schema_version=1", CodeSchema},
		"retention":        {"UPDATE payment_meta SET sales_retention_days=90", CodeSchema},
		"tax_behavior":     {"UPDATE payment_meta SET tax_behavior='inclusive'", CodeSchema},
		"empty_meta":       {"DELETE FROM payment_meta", CodeSchema},
		"unknown_index":    {"CREATE INDEX payment_unknown ON payment_sales(account_id)", CodeSchema},
		"partial":          {"DROP INDEX payment_purchases_prune", CodeSchema},
		"mixed":            {"DROP TABLE payment_purchases; " + strings.TrimSpace(strings.Split(paymentSchemaV1, ";")[2]), CodeSchema},
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			s, dir, now := livePaymentService(t)
			p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
			r, err := s.FundStripePurchase(background, liveFundingEvidence(p, 1, "43"))
			must(t, r, err)
			db, err := connect(filepath.Join(dir, "data.db"), false)
			if err != nil {
				t.Fatal("live corruption connection")
			}
			_, err = db.NewQuery("PRAGMA foreign_keys=OFF; PRAGMA ignore_check_constraints=ON; " + mutation.sql).Execute()
			db.Close()
			if err != nil {
				t.Fatal("live corruption fixture")
			}
			codeIs(t, s.InitializeLiveStripePayments(background), mutation.code)
			s.Close()
			opened, err := Open(background, dir)
			if opened != nil {
				opened.Close()
				t.Fatal("corrupt live state admitted")
			}
			codeIs(t, err, mutation.code)
		})
	}
}

func TestLivePaymentsSharedCutoffAndReplay(t *testing.T) {
	s, dir, now := livePaymentService(t)
	p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
	e := liveFundingEvidence(p, 1, "43")
	// Payment near Checkout expiry must not refresh issuance-based retention.
	e.ChargeCreatedSeconds = e.Session.ExpiresSeconds - 1
	advance(t, s, e.ChargeCreatedSeconds*1000)
	r, err := s.FundStripePurchase(background, e)
	must(t, r, err)
	advance(t, s, int64(p.UnlinkAt)-1)
	purged, err := s.PurgeLiveStripePayments(background)
	if must(t, purged, err) != (PaymentPurgeResult{}) {
		t.Fatal("pair pruned before shared cutoff")
	}
	r, err = s.FundStripePurchase(background, e)
	if must(t, r, err).Status != PaymentDuplicate {
		t.Fatal("pre-cutoff receipt unavailable")
	}
	advance(t, s, int64(p.UnlinkAt))
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentExpired)
	purged, err = s.PurgeLiveStripePayments(background)
	if must(t, purged, err) != (PaymentPurgeResult{Pruned: 2}) {
		t.Fatal("pair not pruned at exact cutoff")
	}
	if purchases, sales, _ := paymentCounts(t, s); purchases != 0 || sales != 0 {
		t.Fatal("half pair survived")
	}
	advance(t, s, int64(p.UnlinkAt)+1)
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentMissing)
	_, err = s.PreparePayment(background, PreparePaymentInput{AccountID: p.AccountID, Denomination: "usd5", Form: paymentForm(now, 1), TaxCode: fixtureTaxCode})
	codeIs(t, err, CodePaymentExpired)
	fresh := claimPurchase(t, s, prepareLivePurchase(t, s, int64(p.UnlinkAt)+1, 2))
	e.Session.Reference = fresh.Reference
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentConflict)
	s.Close()
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	e.Session.Reference = p.Reference
	_, err = reopened.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentMissing)
	if balance(t, reopened, p.AccountID).Available != "5000000" {
		t.Fatal("prune/replay changed credit")
	}
}

func pruneLiveBudget(t *testing.T, s *Service, limit int) int {
	t.Helper()
	var pruned int
	err := s.run(background, func(tx *transaction) error {
		var err error
		pruned, err = tx.prunePayments(limit)
		return err
	}, true)
	if err != nil {
		t.Fatalf("budget prune: %s", Failure(err).Code)
	}
	return pruned
}
func TestLivePaymentsPairedBudgetAndMixedProgress(t *testing.T) {
	for _, limit := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			s, _, now := livePaymentService(t)
			p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
			r, err := s.FundStripePurchase(background, liveFundingEvidence(p, 1, "43"))
			must(t, r, err)
			advance(t, s, int64(p.UnlinkAt))
			got := pruneLiveBudget(t, s, limit)
			want, rows := 0, 1
			if limit == 2 {
				want, rows = 2, 0
			}
			if got != want {
				t.Fatal("paired budget exceeded or underused")
			}
			if purchases, sales, _ := paymentCounts(t, s); purchases != rows || sales != rows {
				t.Fatal("pair was split")
			}
		})
	}
	s, _, now := livePaymentService(t)
	p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
	r, err := s.FundStripePurchase(background, liveFundingEvidence(p, 1, "43"))
	must(t, r, err)
	// Later issuance guarantees the funded pair is at the head of the queue.
	advance(t, s, now+1)
	prepareLivePurchase(t, s, now+1, 2)
	advance(t, s, now+paymentLinkMS+1)
	if pruneLiveBudget(t, s, 1) != 1 {
		t.Fatal("funded head blocked later one-slot row")
	}
	if purchases, sales, _ := paymentCounts(t, s); purchases != 1 || sales != 1 {
		t.Fatal("mixed one-slot prune split funded pair")
	}
	if pruneLiveBudget(t, s, 2) != 2 {
		t.Fatal("mixed pair did not drain")
	}
}

func TestLivePaymentsPairDeleteCancellationRollsBack(t *testing.T) {
	s, _, now := livePaymentService(t)
	p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
	r, err := s.FundStripePurchase(background, liveFundingEvidence(p, 1, "43"))
	must(t, r, err)
	advance(t, s, int64(p.UnlinkAt))
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	db := s.app.NonconcurrentDB().(*dbx.DB)
	original := db.ExecLogFunc
	defer func() { db.ExecLogFunc = original }()
	deletedPurchase := false
	db.ExecLogFunc = func(_ context.Context, _ time.Duration, statement string, _ sql.Result, err error) {
		if err == nil && strings.HasPrefix(statement, "DELETE FROM payment_purchases WHERE") {
			deletedPurchase = true
			cancel()
		}
	}
	out, err := s.PurgeLiveStripePayments(ctx)
	db.ExecLogFunc = original
	codeIs(t, err, CodeWork)
	if !deletedPurchase || out != (PaymentPurgeResult{}) {
		t.Fatal("pair-delete fault seam not exercised or supplied receipt")
	}
	if purchases, sales, _ := paymentCounts(t, s); purchases != 1 || sales != 1 {
		t.Fatal("failure between pair deletes did not roll back")
	}
	out, err = s.PurgeLiveStripePayments(background)
	if must(t, out, err) != (PaymentPurgeResult{Pruned: 2}) {
		t.Fatal("rolled-back pair not drainable")
	}
}

func TestLivePaymentsCombinedCleanupAndStartupDrain(t *testing.T) {
	s, dir, now := livePaymentService(t)
	// 127 generic credit markers leave only one payment deletion slot.
	for i := 0; i < CleanupLimit-1; i++ {
		c, err := s.Credit(background, CreditInput{"paymentaccount0", "1", key(now, i+1000)})
		must(t, c, err)
	}
	p := claimPurchase(t, s, prepareLivePurchase(t, s, now, 1))
	r, err := s.FundStripePurchase(background, liveFundingEvidence(p, 1, "43"))
	must(t, r, err)
	prepareLivePurchase(t, s, now, 2)
	advance(t, s, now+paymentLinkMS)
	out, err := s.Cleanup(background, true)
	if must(t, out, err).Pruned != CleanupLimit {
		t.Fatal("mixed cleanup did not use remaining one slot")
	}
	if purchases, sales, credits := paymentCounts(t, s); purchases != 1 || sales != 1 || credits != 0 {
		t.Fatal("cleanup exceeded combined limit or split pair")
	}
	out, err = s.Cleanup(background, true)
	if must(t, out, err).Pruned != 2 {
		t.Fatal("cleanup did not finish pair")
	}
	// A full startup batch can always progress through funded pairs. Sale dates
	// are only 20 days old, so the legacy 90-day cutoff cannot drive More.
	now += paymentLinkMS
	for i := 1; i <= CleanupLimit/2+1; i++ {
		p := claimPurchase(t, s, prepareLivePurchase(t, s, now, i+10))
		r, err := s.FundStripePurchase(background, liveFundingEvidence(p, i+10, "0"))
		must(t, r, err)
	}
	advance(t, s, now+paymentLinkMS)
	s.Close()
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	purged, err := reopened.PurgeLiveStripePayments(background)
	if must(t, purged, err) != (PaymentPurgeResult{Pruned: CleanupLimit, More: true}) {
		t.Fatal("startup full batch did not expose remaining expired pair")
	}
	purged, err = reopened.PurgeLiveStripePayments(background)
	if must(t, purged, err) != (PaymentPurgeResult{Pruned: 2}) {
		t.Fatal("startup pair backlog did not terminate")
	}
	if purchases, sales, _ := paymentCounts(t, reopened); purchases != 0 || sales != 0 {
		t.Fatal("startup retained expired live sale")
	}
}
