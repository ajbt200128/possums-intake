package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// Closed, locally authored request diagnostics. These are distinct from ledger
// diagnostics because parsing/authentication happens before accounting. They
// never echo the request, the key or a raw parser error.
var (
	diagUnauthorized = diagnostic{
		kind: failuretype.AuthenticationRefused,
		Code: "LEDGER_HTTP_UNAUTHORIZED", Stage: "authentication",
		Constraint: "shared_bearer_admin_key", Outcome: "no_effect",
		NextStep: "Supply the shared runtime key as a single Authorization: Bearer header. Do not place it in a URL.",
	}
	diagNotFound = diagnostic{
		kind: failuretype.PolicyRefused,
		Code: "LEDGER_HTTP_NOT_FOUND", Stage: "routing",
		Constraint: "custom_accounting_routes_only", Outcome: "no_effect",
		NextStep: "Use a defined custom accounting route. No native framework, installer, dashboard or CRUD route is exposed.",
	}
	diagQuery = diagnostic{
		kind: failuretype.RequestInvalid,
		Code: "LEDGER_HTTP_QUERY_REJECTED", Stage: "validation",
		Constraint: "no_query_parameters", Outcome: "no_effect",
		NextStep: "Send one JSON object body with no query string.",
	}
	diagContentEncoding = diagnostic{
		kind: failuretype.RequestInvalid,
		Code: "LEDGER_HTTP_ENCODING_REJECTED", Stage: "validation",
		Constraint: "identity_content_encoding", Outcome: "no_effect",
		NextStep: "Send the JSON body without compression.",
	}
	diagContentType = diagnostic{
		kind: failuretype.RequestInvalid,
		Code: "LEDGER_HTTP_CONTENT_TYPE_REJECTED", Stage: "validation",
		Constraint: "application_json", Outcome: "no_effect",
		NextStep: "Set one Content-Type: application/json header; standard media-type parameters are accepted.",
	}
	diagDuplicateHeader = diagnostic{
		kind: failuretype.RequestInvalid,
		Code: "LEDGER_HTTP_HEADER_REJECTED", Stage: "validation",
		Constraint: "singleton_headers", Outcome: "no_effect",
		NextStep: "Send singleton Authorization, Content-Type, Content-Encoding and Idempotency-Key headers as applicable.",
	}
	diagIdempotency = diagnostic{
		kind: failuretype.IdempotencyRefused,
		Code: "LEDGER_HTTP_IDEMPOTENCY_REQUIRED", Stage: "validation",
		Constraint: "single_idempotency_key_header", Outcome: "no_effect",
		NextStep: "Send exactly one immutable Idempotency-Key header for this operation.",
	}
	diagInput = diagnostic{
		kind: failuretype.RequestInvalid,
		Code: "LEDGER_HTTP_INPUT_REJECTED", Stage: "validation",
		Constraint: "single_object_required_fields", Outcome: "no_effect",
		NextStep: "Send one JSON object with correctly typed required fields, no duplicate keys or trailing data. Extra fields are ignored.",
	}
	diagUnknown = diagnostic{
		kind: failuretype.RequestDispatchUnknown,
		Code: "LEDGER_HTTP_OUTCOME_UNKNOWN", Stage: "request_dispatch",
		Constraint: "closed_ledger_diagnostic", Outcome: "unknown",
		NextStep: "Dispatch failed; underlying cause unknown. Previously observed accounting outcomes are unchanged. Delivery is not an accounting receipt; resolve uncertainty using the original identity, never a new one.",
	}
	diagRead = diagnostic{
		kind: failuretype.RequestReadUnknown,
		Code: "LEDGER_HTTP_READ_UNAVAILABLE", Stage: "request_read",
		Constraint: "complete_bounded_body", Outcome: "no_effect",
		NextStep: "Request reading failed; underlying cause unknown. No accounting method ran. Check the transport privately; preserve the original identity.",
	}
)

// statusFor pins each closed ledger code to a status. Known rolled-back/no-effect
// and unknown outcomes are deliberately different.
func statusFor(code ledger.Code) int {
	switch code {
	case ledger.CodeInput, ledger.CodeKey:
		return http.StatusBadRequest
	case ledger.CodeAccount:
		return http.StatusNotFound
	case ledger.CodeInsufficient, ledger.CodeDisabled, ledger.CodeConflict, ledger.CodeCharge, ledger.CodeExpired, ledger.CodeOverflow, ledger.CodeKeyWindow:
		return http.StatusConflict
	case ledger.CodeEvidence:
		return http.StatusGone
	case ledger.CodeWriter, ledger.CodeWork, ledger.CodeBegin, ledger.CodeStorage, ledger.CodeRandom,
		ledger.CodeCorrupt, ledger.CodeCommit, ledger.CodeClosed, ledger.CodeClose,
		ledger.CodeStartup, ledger.CodeMissing, ledger.CodeExisting, ledger.CodeSchema, ledger.CodePath,
		ledger.CodeRoutes:
		return http.StatusServiceUnavailable
	default:
		return http.StatusServiceUnavailable
	}
}

// decode reads one bounded object, requiring known fields and ignoring extras.
// When requireAll is true every field in the target must be present and non-null.
func (h *handler) decode(w http.ResponseWriter, r *http.Request, target any, requireAll bool) bool {
	body, kind, expected, interruption := readBounded(r)
	if b, ok := w.(*responseBuffer); ok {
		b.requestInterruption = interruption
	}
	if kind.Valid() {
		if kind == failuretype.RequestInvalid {
			writeDiagnostic(w, http.StatusBadRequest, diagInput)
		} else {
			d := diagRead
			d.kind = kind
			if expected {
				writeJSON(w, http.StatusServiceUnavailable, d)
			} else {
				writeDiagnostic(w, http.StatusServiceUnavailable, d)
			}
		}
		return false
	}
	if decodeStrict(body, target, requireAll) != nil {
		writeDiagnostic(w, http.StatusBadRequest, diagInput)
		return false
	}
	return true
}

// Discard raw I/O causes only after inspecting supported evidence. Incomplete
// framing is invalid input; other read failures/panics are not guessed syntax.
func readBounded(r *http.Request) (body []byte, kind failuretype.Type, expected bool, interruption failuretype.Type) {
	defer func() {
		if value := recover(); value != nil {
			body = nil
			err, _ := value.(error)
			kind = ioFailureKind(err, failuretype.RequestReadUnknown)
			expected = expectedHTTPError(err)
			if !expected {
				interruption = matchingRequestInterruption(kind, r)
			}
		}
	}()
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if len(body) > MaxBodyBytes || invalidRead(err) {
		return nil, failuretype.RequestInvalid, false, 0
	}
	if err != nil {
		kind = ioFailureKind(err, failuretype.RequestReadUnknown)
		expected = expectedHTTPError(err)
		if !expected {
			interruption = matchingRequestInterruption(kind, r)
		}
		return nil, kind, expected, interruption
	}
	return body, 0, false, 0
}

func invalidRead(err error) (invalid bool) {
	defer func() { _ = recover() }()
	var oversized *http.MaxBytesError
	return safeErrorIs(err, io.ErrUnexpectedEOF) || (errors.As(err, &oversized) && oversized != nil)
}

// decodeStrict rejects empty bodies, trailing data, duplicate object keys,
// nulls for required fields and JSON that is not exactly one object. Extra
// fields are ignored, not bound to accounting inputs. It uses only the standard token decoder and the target's json tags.
func decodeStrict(body []byte, target any, requireAll bool) error {
	if !utf8.Valid(body) || !json.Valid(body) {
		return errNotObject
	}
	decoder := json.NewDecoder(bytesReader(body))
	if err := expectOneObject(decoder, target, requireAll); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errTrailing
	}
	return nil
}

// json.Valid establishes escape syntax first. The standard decoder accepts lone
// UTF-16 surrogates by replacing them; opaque account identities must not change.
func validUnicodeEscapes(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		i++
		if body[i] != 'u' {
			continue // includes escaped backslashes; their following text is literal
		}
		value, _ := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
		i += 4
		switch {
		case value >= 0xdc00 && value <= 0xdfff:
			return false
		case value >= 0xd800 && value <= 0xdbff:
			if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func expectOneObject(decoder *json.Decoder, target any, requireAll bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errNotObject
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errNotObject
		}
		if _, duplicate := fields[key]; duplicate {
			return errDuplicate
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		fields[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return bindFields(fields, target, requireAll)
}
