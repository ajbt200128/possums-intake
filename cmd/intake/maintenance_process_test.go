package main

import (
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

// This intentionally uses the real 10s/60s production schedule. Direct SQL is
// restricted to a synthetic fixture and read-only inspection, not another
// PocketBase authority or a production clock override.
func TestProcessScheduledExpiryPruningAndCachedHealth(t *testing.T) {
	dir := initProcessState(t)
	p := startProcess(t, dir, syntheticAddress(t))
	successRPC(t, p, "/accounts/provision", `{"account_id":"synthetic-a"}`, "")
	successRPC(t, p, "/credits", `{"account_id":"synthetic-a","amount":"2"}`, processKey(1))
	row := readReservation(t, successRPC(t, p, "/reservations", `{"account_id":"synthetic-a","maximum":"1","timeout_seconds":1}`, processKey(2)))
	db, err := sql.Open("sqlite", filepath.Join(dir, "data.db")+"?mode=rw&_pragma=busy_timeout(250)")
	if err != nil {
		t.Fatal("synthetic state inspector unavailable")
	}
	defer db.Close()
	lock, err := db.Begin()
	if err != nil {
		t.Fatal("health lock fixture failed")
	}
	defer lock.Rollback()
	if _, err := lock.Exec("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms WHERE singleton=1"); err != nil {
		t.Fatal("health lock acquisition failed")
	}
	var before, after int64
	if db.QueryRow("SELECT clock_high_water_ms FROM ledger_meta WHERE singleton=1").Scan(&before) != nil {
		t.Fatal("watermark inspection failed")
	}
	client := &http.Client{Timeout: time.Second}
	for n := 0; n < 20; n++ {
		response, err := client.Get(p.base + "/healthz")
		if err != nil {
			t.Fatal("cached health request failed")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("usable storage not ready")
		}
	}
	if db.QueryRow("SELECT clock_high_water_ms FROM ledger_meta WHERE singleton=1").Scan(&after) != nil || before != after {
		t.Fatal("health performed accounting or changed the clock")
	}
	if lock.Rollback() != nil {
		t.Fatal("health lock release failed")
	}

	// Advance only the disposable database clock: a tick must expire this hold
	// without any balance/lookup call doing the work, then prune on the 60s tick.
	future := time.Now().UnixMilli() + ledger.KeyWindowMS + 10_000
	if _, err := db.Exec("UPDATE ledger_meta SET clock_high_water_ms=? WHERE singleton=1", future); err != nil {
		t.Fatal("synthetic clock fixture failed")
	}
	deadline := time.NewTimer(75 * time.Second)
	defer deadline.Stop()
	ticks := time.NewTicker(100 * time.Millisecond)
	defer ticks.Stop()
	start := time.Now()
	expired := false
	for {
		select {
		case <-deadline.C:
			t.Fatal("scheduled expiry/pruning did not complete")
		case <-ticks.C:
			var state string
			err := db.QueryRow("SELECT state FROM ledger_reservations WHERE reservation_id=?", row.ID).Scan(&state)
			if err == nil && state == "expired" && !expired {
				expired = true
				if time.Since(start) > 15*time.Second {
					t.Fatal("expiry did not use the 10s schedule")
				}
			}
			if err == sql.ErrNoRows {
				if !expired || time.Since(start) < 45*time.Second {
					t.Fatal("pruning bypassed terminal state or the 60s schedule")
				}
				var credits int
				if db.QueryRow("SELECT count(*) FROM ledger_credits").Scan(&credits) != nil || credits != 0 {
					t.Fatal("60s tick did not prune credit markers")
				}
				stopProcess(t, p)
				inspectPrivateState(t, dir)
				return
			}
			if err != nil {
				t.Fatal("scheduled state inspection failed")
			}
		}
	}
}
