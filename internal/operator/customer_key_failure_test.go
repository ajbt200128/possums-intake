package operator

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

func TestCustomerKeyProjectionIsClosed(t *testing.T) {
	for _, code := range []ledger.Code{"SITE_KEY_INPUT", "SITE_KEY_UNAVAILABLE"} {
		constraint, status := "gateway_key_and_account_format", 400
		if code == "SITE_KEY_UNAVAILABLE" {
			constraint, status = "preparation_cause_unknown", 503
		}
		d := &ledger.Diagnostic{Code: code, Stage: "customer_key_prepare", Constraint: constraint, Outcome: "no_effect", NextStep: "SYNTHETIC_SECRET_EXCEPTION_SENTINEL"}
		w := httptest.NewRecorder()
		if !customerKeyFailure(w, 500, d) || w.Code != status || strings.Contains(w.Body.String(), "SYNTHETIC_SECRET_EXCEPTION_SENTINEL") {
			t.Fatal("closed key preparation projection mismatch")
		}
		var response struct {
			Code    ledger.Code `json:"code"`
			Outcome string      `json:"outcome"`
			Message string      `json:"message"`
			Next    string      `json:"next_step"`
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Code != code || response.Outcome != "no_effect" || response.Message == "" || response.Next == "" {
			t.Fatal("key preparation diagnostic incomplete")
		}
		for _, change := range []string{"code", "stage", "constraint", "outcome"} {
			bad := *d
			switch change {
			case "code":
				bad.Code = "UNREVIEWED_CODE"
			case "stage":
				bad.Stage = "unreviewed_stage"
			case "constraint":
				bad.Constraint = "unreviewed_constraint"
			case "outcome":
				bad.Outcome = "unknown"
			}
			if customerKeyFailure(httptest.NewRecorder(), 500, &bad) {
				t.Fatal("unreviewed key preparation claim projected")
			}
		}
	}
}
