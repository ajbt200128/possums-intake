package operator

import (
	"encoding/json"
	"errors"
	"net/http"

	"possums-intake/internal/ledger"
)

// Only the website publication's closed protocol crosses the stock error
// serializer. Never expose arbitrary Diagnostic fields or upstream causes.
func websitePublicationFailure(w http.ResponseWriter, status int, err error) (handled bool) {
	defer func() {
		if value := recover(); value != nil {
			if handled {
				panic(value)
			}
			handled = false
		}
	}()
	var d *ledger.Diagnostic
	if !errors.As(err, &d) || d == nil {
		return false
	}
	message, next := "", ""
	switch d.Code {
	case "SITE_PUBLICATION_REFUSED":
		if d.Stage != "publication_prepare" || d.Constraint != "bounded_renderable_code" || d.Outcome != "no_effect" {
			return false
		}
		message = "Website preparation refused; no publication occurred."
		next = "Check the trusted draft syntax, host version, routes and rendering bounds privately; active code is unchanged."
	case "SITE_PUBLICATION_UNAVAILABLE":
		if d.Constraint != "authoritative_code_selection_cause_unknown" {
			return false
		}
		switch d.Stage {
		case "publication_admission":
			if d.Outcome != "no_effect" {
				return false
			}
		case "publication_commit", "publication_dispatch", "publication_swap":
			if d.Outcome != "website_selection_unconfirmed" {
				return false
			}
		case "publication_completion":
			if d.Outcome != "website_selection_committed_response_unconfirmed" {
				return false
			}
		default:
			return false
		}
		message = "Website publication unavailable; underlying cause unknown."
		next = "Inspect selected code privately before another publish; new website admission may be closed. No operation was replayed."
		if d.Stage == "publication_admission" {
			next = "Publish using the stock single-record editor, outside a batch transaction; no publication occurred."
		}
		if d.Stage == "publication_completion" {
			next = "Code selection committed; response completion is unconfirmed. Reload the website instead of replaying publication."
		}
	default:
		return false
	}
	handled = true
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
	}{d.Code, d.Stage, d.Constraint, d.Outcome, status, message, map[string]string{}, next})
	return true
}
