package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
)

func nativeCreate(t *testing.T, s *Service, account, amount string) string {
	t.Helper()
	raw, err := s.NativeAccountCreate(background, NativeAccountCreate{account, Money(amount), true})
	if err != nil {
		t.Fatal("synthetic native create unavailable")
	}
	var record struct {
		ID      string `json:"id"`
		Balance string `json:"balance"`
	}
	if json.Unmarshal(raw, &record) != nil || !nativeIDPattern.MatchString(record.ID) || record.Balance != amount {
		t.Fatal("native record JSON differs")
	}
	return record.ID
}
func pointer[T any](v T) *T { return &v }

func TestNativeAccountCanonicalMoneyIdentityHoldsAndExpiry(t *testing.T) {
	s, _, now := testService(t)
	id := nativeCreate(t, s, "native", "18446744073709551615")
	_, err := s.Credit(background, CreditInput{"native", "1", key(now, 80)})
	codeIs(t, err, CodeOverflow)
	for _, value := range []Money{"01", "-1", "18446744073709551616", "1.0", ""} {
		raw, err := s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: &value})
		codeIs(t, err, CodeInput)
		if raw != nil {
			t.Fatal("invalid edit returned receipt")
		}
	}
	for _, patch := range []NativeAccountPatch{{ID: pointer("aaaaaaaaaaaaaaa")}, {AccountID: pointer("different")}} {
		_, err := s.NativeAccountUpdate(background, id, patch)
		codeIs(t, err, CodeInput)
	}
	// Native validation hooks preserve identity even through supported Record API.
	record, err := s.app.FindRecordById(AccountCollectionID, id)
	if err != nil {
		t.Fatal("record unavailable")
	}
	record.Set("account_id", "different")
	if err := s.app.Save(record); err == nil {
		t.Fatal("native validation allowed identity change")
	}
	record, err = s.app.FindRecordById(AccountCollectionID, id)
	if err != nil {
		t.Fatal("record unavailable")
	}
	record.Id = "aaaaaaaaaaaaaaa"
	if err := s.app.Save(record); err == nil {
		t.Fatal("native validation allowed internal identity change")
	}
	r := reserve(t, s, ReserveInput{"native", "10", 1, key(now, 81)})
	_, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("20"))})
	codeIs(t, err, CodeNativeHold)
	raw, err := s.NativeAccountUpdate(background, id, NativeAccountPatch{Enabled: pointer(false)})
	if err != nil || raw == nil {
		t.Fatal("enabled edit blocked by active hold")
	}
	if balance(t, s, "native").AccountBalance.Enabled {
		t.Fatal("disabled edit not saved")
	}
	// Repeating a current total is permitted, but hold consistency still checked.
	_, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("18446744073709551615"))})
	if err != nil {
		t.Fatal("unchanged total rejected")
	}
	advance(t, s, int64(r.ExpiresAt))
	_, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("2"))})
	if err != nil {
		t.Fatal("expired hold did not release native balance edit")
	}
	row, err := s.Lookup(background, r.ReservationID)
	if err != nil || row.State != Expired || balance(t, s, "native").Available != "2" {
		t.Fatal("native expiry inconsistent")
	}
}

func TestNativeAccountWritesSerializeWithFinancialOperations(t *testing.T) {
	for _, op := range []string{"reserve", "credit", "settle"} {
		t.Run(op, func(t *testing.T) {
			s, _, now := testService(t)
			id := nativeCreate(t, s, "race", "100")
			var r Reservation
			if op == "settle" {
				r = reserve(t, s, ReserveInput{"race", "20", 600, key(now, 30)})
			}
			var wg sync.WaitGroup
			wg.Add(2)
			results := make(chan error, 2)
			go func() {
				defer wg.Done()
				_, err := s.NativeAccountUpdate(background, id, NativeAccountPatch{Enabled: pointer(false)})
				results <- err
			}()
			go func() {
				defer wg.Done()
				var err error
				switch op {
				case "reserve":
					_, err = s.Reserve(background, ReserveInput{"race", "20", 600, key(now, 31)})
				case "credit":
					_, err = s.Credit(background, CreditInput{"race", "5", key(now, 32)})
				case "settle":
					_, err = s.Settle(background, SettleInput{r.ReservationID, "10"})
				}
				results <- err
			}()
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil && !(op == "reserve" && Failure(err).Code == CodeDisabled) {
					t.Fatal("serialized native race failed")
				}
			}
			snapshot := balance(t, s, "race")
			want := Money("100")
			if op == "credit" {
				want = "105"
			}
			if op == "settle" {
				want = "90"
			}
			if snapshot.AccountBalance.Balance != want || snapshot.AccountBalance.Enabled {
				t.Fatal("native save overwrote fresh financial state")
			}
		})
	}
	// A simultaneous reserve/absolute reduction cannot produce an underfunded hold.
	s, _, now := testService(t)
	id := nativeCreate(t, s, "absolute", "100")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("5"))})
	}()
	go func() {
		defer wg.Done()
		_, _ = s.Reserve(background, ReserveInput{"absolute", "20", 600, key(now, 40)})
	}()
	wg.Wait()
	snapshot := balance(t, s, "absolute")
	if !(snapshot.AccountBalance.Balance == "5" && snapshot.Available == "5" || snapshot.AccountBalance.Balance == "100" && snapshot.Available == "80") {
		t.Fatal("absolute-edit reserve race violated accounting")
	}
	// Concurrent native/custom create share one unique external identity.
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = s.NativeAccountCreate(background, NativeAccountCreate{"same", "0", true})
	}()
	go func() { defer wg.Done(); _, _ = s.Provision(background, "same") }()
	wg.Wait()
	var count int
	if err := s.app.DB().NewQuery("SELECT count(*) FROM ledger_accounts WHERE account_id='same'").Row(&count); err != nil || count != 1 {
		t.Fatal("create race duplicated account")
	}
}

func TestNativeWriterCancellationRollbackEncodingAndCommitEvidence(t *testing.T) {
	s, _, _ := testService(t)
	id := nativeCreate(t, s, "fault", "10")
	s.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(background, 20*time.Millisecond)
	raw, err := s.NativeAccountUpdate(ctx, id, NativeAccountPatch{Balance: pointer(Money("11"))})
	cancel()
	<-s.gate
	codeIs(t, err, CodeWriter)
	if raw != nil {
		t.Fatal("writer timeout receipt")
	}
	hook := s.app.OnRecordValidate(AccountCollectionName).BindFunc(func(e *core.RecordEvent) error { return errors.New("SYNTHETIC_HOSTILE_RECORD_ERROR") })
	raw, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("11"))})
	codeIs(t, err, CodeStorage)
	if raw != nil || balance(t, s, "fault").Available != "10" {
		t.Fatal("failed save effect escaped rollback")
	}
	s.app.OnRecordValidate().Unbind(hook)
	// Actual pre-commit RecordJSON marshal failure, not a test-only encoder.
	hook = s.app.OnRecordCreateExecute(AccountCollectionName).BindFunc(func(e *core.RecordEvent) error {
		err := e.Next()
		e.Record.WithCustomData(true)
		e.Record.Set("synthetic_unencodable", make(chan int))
		return err
	})
	raw, err = s.NativeAccountCreate(background, NativeAccountCreate{"encode-fail", "10", true})
	codeIs(t, err, CodeStorage)
	if Failure(err).Kind() != failuretype.ResponseEncodingUnknown || Failure(err).Outcome != "no_effect" {
		t.Fatal("actual native encoding failure lost classification or observed rollback")
	}
	if raw != nil {
		t.Fatal("encoding failure returned native receipt")
	}
	s.app.OnRecordCreateExecute().Unbind(hook)
	_, err = s.Balance(background, "encode-fail")
	codeIs(t, err, CodeAccount)
	original := s.app
	s.app = lostCompletionApp{original}
	raw, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("12"))})
	codeIs(t, err, CodeCommit)
	if raw != nil {
		t.Fatal("unknown commit emitted requested native result")
	}
	s.app = original
	if balance(t, s, "fault").Available != "12" {
		t.Fatal("missing evidence fixture failed")
	}
	s.app = afterCommitFailure{original}
	raw, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Enabled: pointer(false)})
	if err != nil || raw == nil {
		t.Fatal("known native completion erased by later failure")
	}
	s.app = afterNativeCommitPanic{original}
	raw, err = s.NativeAccountUpdate(background, id, NativeAccountPatch{Enabled: pointer(true)})
	if err != nil || raw == nil {
		t.Fatal("known native completion erased by postcommit panic")
	}
	s.app = original
	ctx, cancel = context.WithCancel(background)
	hook = s.app.OnRecordValidate(AccountCollectionName).BindFunc(func(e *core.RecordEvent) error { cancel(); return e.Next() })
	raw, err = s.NativeAccountUpdate(ctx, id, NativeAccountPatch{Balance: pointer(Money("13"))})
	codeIs(t, err, CodeWork)
	s.app.OnRecordValidate().Unbind(hook)
	if raw != nil || balance(t, s, "fault").Available != "12" {
		t.Fatal("cancelled native work did not roll back")
	}
}

// Inject at the supported transaction-scoped lookup, not at the public formatter.
type nativeLookupFailureApp struct {
	core.App
	failure error
	bound   *bool
}

func (a nativeLookupFailureApp) RunInTransaction(fn func(core.App) error) error {
	return a.App.RunInTransaction(func(tx core.App) error {
		return fn(nativeLookupFailureApp{tx, a.failure, a.bound})
	})
}

func (a nativeLookupFailureApp) FindRecordById(collection any, id string, filters ...func(*dbx.SelectQuery) error) (*core.Record, error) {
	q := a.App.RecordQuery(collection)
	for _, filter := range filters {
		if err := filter(q); err != nil {
			return nil, err
		}
	}
	if ctx := q.Context(); ctx != nil {
		deadline, ok := ctx.Deadline()
		*a.bound = ok && !deadline.After(time.Now().Add(WorkBudget))
	}
	return nil, a.failure
}

func TestNativeLookupPreservesFailureAndTransactionDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-corrupt.db")
	if err := os.WriteFile(path, []byte("synthetic-not-a-database"), 0600); err != nil {
		t.Fatal("corrupt fixture unavailable")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal("corrupt fixture open unavailable")
	}
	var count int
	corrupt := db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&count)
	_ = db.Close()
	if !fatalStorage(corrupt) {
		t.Fatal("fixture did not produce typed fatal storage evidence")
	}
	for _, test := range []struct {
		name    string
		failure error
		code    Code
		ready   bool
	}{
		{"missing", sql.ErrNoRows, CodeAccount, true},
		{"unknown", errors.New("SYNTHETIC_HOSTILE_LOOKUP_ERROR"), CodeStorage, true},
		{"cancelled", context.Canceled, CodeWork, true},
		{"deadline", context.DeadlineExceeded, CodeWork, true},
		{"fatal_storage", corrupt, CodeStorage, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, _ := testService(t)
			id := nativeCreate(t, s, "lookup-fault", "10")
			original := s.app
			bound := false
			s.app = nativeLookupFailureApp{original, test.failure, &bound}
			raw, err := s.NativeAccountUpdate(background, id, NativeAccountPatch{Balance: pointer(Money("11"))})
			s.app = original
			codeIs(t, err, test.code)
			if raw != nil || !bound || s.Ready() != test.ready {
				t.Fatal("lookup failure lost category, deadline, readiness or receipt isolation")
			}
			diagnosticJSON, marshalErr := json.Marshal(Failure(err))
			if marshalErr != nil || strings.Contains(string(diagnosticJSON), "SYNTHETIC_HOSTILE") {
				t.Fatal("lookup failure exposed hostile detail")
			}
			record, lookupErr := original.FindRecordById(AccountCollectionID, id)
			if lookupErr != nil || record.GetString("balance") != "10" {
				t.Fatal("failed lookup changed stored balance")
			}
		})
	}
}

type afterNativeCommitPanic struct{ core.App }

func (a afterNativeCommitPanic) RunInTransaction(fn func(core.App) error) error {
	if err := a.App.RunInTransaction(fn); err != nil {
		return err
	}
	panic("SYNTHETIC_HOSTILE_POSTCOMMIT_PANIC")
}

func TestInitialSchemaDDLMetadataDriftAndOldLayoutRefusal(t *testing.T) {
	for _, statement := range []string{
		"DROP INDEX ledger_accounts_account_id",
		"CREATE INDEX unwanted_account_index ON ledger_accounts(enabled)",
		"UPDATE _collections SET fields=json_set(fields,'$[2].max',21) WHERE name='ledger_accounts'",
		"UPDATE _collections SET listRule='' WHERE name='ledger_accounts'",
	} {
		t.Run(statement, func(t *testing.T) {
			s, dir, _ := testService(t)
			if err := s.run(background, func(tx *transaction) error { return tx.exec(statement, nil) }, false); err != nil {
				t.Fatal("drift fixture failed")
			}
			if s.Close() != nil {
				t.Fatal("close fixture failed")
			}
			reopened, err := Open(background, dir)
			if reopened != nil {
				reopened.Close()
			}
			codeIs(t, err, CodeSchema)
		})
	}
	// Earlier disposable ordinary-SQL account layouts used the same unreleased
	// schema number. Exact DDL/metadata checks must reject them, not upgrade them.
	s, dir, _ := testService(t)
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec(`DROP TABLE ledger_accounts;
CREATE TABLE ledger_accounts (
 account_id TEXT PRIMARY KEY NOT NULL CHECK(length(CAST(account_id AS BLOB)) BETWEEN 1 AND 128),
 balance TEXT NOT NULL CHECK(typeof(balance)='text' AND length(balance) BETWEEN 1 AND 20 AND balance NOT GLOB '*[^0-9]*' AND (balance='0' OR substr(balance,1,1) BETWEEN '1' AND '9')),
 enabled INTEGER NOT NULL CHECK(typeof(enabled)='integer' AND enabled IN (0,1))
);
DELETE FROM _collections WHERE name='ledger_accounts';
INSERT INTO ledger_accounts(account_id,balance,enabled) VALUES('old-disposable','17',1);`, nil)
	}, false); err != nil {
		t.Fatal("old disposable layout fixture failed")
	}
	if s.Close() != nil {
		t.Fatal("close fixture failed")
	}
	before := map[string][32]byte{}
	for _, name := range []string{"data.db", "auxiliary.db"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal("fixture read failed")
		}
		before[name] = sha256.Sum256(b)
	}
	_, err := Open(background, dir)
	codeIs(t, err, CodeSchema)
	for name, want := range before {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || sha256.Sum256(b) != want {
			t.Fatal("old disposable layout was mutated or silently rebuilt")
		}
	}
	// Explicitly selecting NEW empty disposable state produces the final layout;
	// Init on the old directory still refuses it. No implicit reset or migration.
	codeIs(t, Init(background, dir), CodeExisting)
	_, freshDir, _ := testService(t)
	// Direct checks exercise the actual final DDL, not the refused old table.
	db, err := connect(filepath.Join(freshDir, "data.db"), false)
	if err != nil {
		t.Fatal("fixture connect failed")
	}
	defer db.Close()
	for _, query := range []string{
		"INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('bad','bad','0',1)",
		"INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('aaaaaaaaaaaaaaa','a','18446744073709551616',1)",
		"INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('aaaaaaaaaaaaaaa','a','01',1)",
		"INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('aaaaaaaaaaaaaaa','a','0',2)",
	} {
		if _, err := db.NewQuery(query).Execute(); err == nil {
			t.Fatal("versioned DDL constraint missing")
		}
	}
}
