package payments

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"possums-intake/internal/stripepay"
)

func TestIntegratedProviderDetailsNeverPersistOrReachDiagnosticOutput(t *testing.T) {
	f := newFixture(t, true)
	captureNoOutput(t, func() {
		ref := f.start(t)
		f.paid()
		assertStatus(t, f.service, ref, "awaiting_payment")
		code, reply := f.deliver(t, event(ref))
		if code != 200 || reply.Status != "funded" {
			t.Fatal("funding")
		}
		code, reply = f.deliver(t, event(ref))
		if code != 200 || reply.Status != "already_funded" {
			t.Fatal("duplicate")
		}
		p, err := f.core.GetPayment(context.Background(), testAccount, ref)
		if err != nil {
			t.Fatal(err)
		}
		private := []string{privateSentinel, stripepay.FixtureAPIKey, stripepay.FixtureWebhookSecret, f.provider.URL, "http://", "https://"}
		checkTableValues(t, f.dir, "data.db", "payment_purchases", private)
		// Sales and generic credit/reservation tables may not preserve ANY bridge.
		private = append(private, ref, p.FormAttempt, p.CreateKey, testSession, testIntent, testCharge)
		for _, table := range []string{"payment_sales", "ledger_credits", "ledger_reservations"} {
			checkTableValues(t, f.dir, "data.db", table, private)
		}
		checkTableValues(t, f.dir, "auxiliary.db", "_logs", private)
	})
}

func checkTableValues(t *testing.T, dir, dbName, table string, private []string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, dbName))
	if err != nil {
		t.Fatal("privacy fixture database")
	}
	defer db.Close()
	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal("privacy fixture query")
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal("privacy fixture columns")
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal("privacy fixture scan")
		}
		for _, value := range values {
			text := fmt.Sprint(value)
			if b, ok := value.([]byte); ok {
				text = string(b)
			}
			for _, secret := range private {
				if strings.Contains(text, secret) {
					t.Fatal("forbidden provider field/bridge in logical database")
				}
			}
		}
	}
	if rows.Err() != nil {
		t.Fatal("privacy fixture rows")
	}
}

func captureNoOutput(t *testing.T, fn func()) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal("capture fixture")
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	output := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(reader); output <- b }()
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		_ = writer.Close()
		b := <-output
		_ = reader.Close()
		if len(b) != 0 {
			t.Error("integrated path emitted diagnostic output; bytes withheld")
		}
	}()
	fn()
}
