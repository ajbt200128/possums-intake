package operator

import (
	"encoding/json"
	"errors"
	"net/http"

	"possums-intake/internal/ledger"
)

func customerKeyFailure(w http.ResponseWriter, status int, err error) bool {
	var d *ledger.Diagnostic
	if !errors.As(err, &d) || d == nil || d.Stage != "customer_key_prepare" || d.Outcome != "no_effect" {
		return false
	}
	message, next := "", ""
	switch d.Code {
	case "SITE_KEY_INPUT":
		if d.Constraint != "gateway_key_and_account_format" {
			return false
		}
		status = http.StatusBadRequest
		message = "Customer-key preparation refused; no credential was saved by this step."
		next = "Use a privately generated gateway-format key in both password fields and the exact ledger account identity; do not share input values."
	case "SITE_KEY_UNAVAILABLE":
		if d.Constraint != "preparation_cause_unknown" {
			return false
		}
		status = http.StatusServiceUnavailable
		message = "Customer-key preparation unavailable; underlying cause unknown."
		next = "Inspect the native form privately before another attempt; no credential was saved by this preparation step. Do not share credentials."
	default:
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code       ledger.Code       `json:"code"`
		Stage      string            `json:"stage"`
		Constraint string            `json:"constraint"`
		Outcome    string            `json:"outcome"`
		Status     int               `json:"status"`
		Message    string            `json:"message"`
		Data       map[string]string `json:"data"`
		NextStep   string            `json:"next_step"`
	}{d.Code, "customer_key_prepare", d.Constraint, "no_effect", status, message, map[string]string{}, next})
	return true
}
