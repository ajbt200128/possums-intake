package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

type nativeInputKey struct{}
type nativeInput struct {
	Create ledger.NativeAccountCreate
	Patch  ledger.NativeAccountPatch
}

func accountRoute(path string) (string, bool) {
	for _, name := range []string{ledger.AccountCollectionName, ledger.AccountCollectionID} {
		prefix := "/api/collections/" + name + "/records"
		if path == prefix {
			return "", true
		}
		if strings.HasPrefix(path, prefix+"/") {
			id := strings.TrimPrefix(path, prefix+"/")
			if recordID.MatchString(id) {
				return id, true
			}
		}
	}
	return "", false
}

// This executes before PB RequestInfo/body binding, ReplaceModifiers and field
// coercion. Only the upstream editor's actual JSON envelope is accepted.
func strictAccountBody(req *http.Request) (nativeInput, bool) {
	var input nativeInput
	kind, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || len(params) != 0 || req.Header.Get("Content-Encoding") != "" {
		return input, false
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, 4097))
	if err != nil {
		kind := ledger.FailureKind(err, failuretype.RequestReadUnknown)
		if o := requestOwner(req); o != nil {
			o.inputKind = kind
		}
		return input, false
	}
	if len(raw) > 4096 || !utf8.Valid(raw) || !json.Valid(raw) || !validUnicodeEscapes(raw) {
		return input, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return input, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return input, false
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return input, false
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return input, false
		}
		switch key {
		case "account_id", "balance", "id", "collectionId", "collectionName":
			var text string
			if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil {
				return input, false
			}
			switch key {
			case "account_id":
				input.Create.AccountID = text
				input.Patch.AccountID = &text
			case "balance":
				money, err := ledger.ParseMoney(text)
				if err != nil {
					return input, false
				}
				input.Create.Balance = money
				input.Patch.Balance = &money
			case "id":
				if req.Method == http.MethodPost {
					if text != "" {
						return input, false
					}
				} else {
					input.Patch.ID = &text
				}
			case "collectionId":
				if text != ledger.AccountCollectionID {
					return input, false
				}
			case "collectionName":
				if text != ledger.AccountCollectionName {
					return input, false
				}
			}
		case "enabled":
			if string(value) != "true" && string(value) != "false" {
				return input, false
			}
			enabled := string(value) == "true"
			input.Create.Enabled = enabled
			input.Patch.Enabled = &enabled
		default:
			return input, false
		}
	}
	if _, err := decoder.Token(); err != nil {
		return input, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return input, false
	}
	if req.Method == http.MethodPost && (!seen["account_id"] || !seen["balance"] || !seen["enabled"]) {
		return input, false
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	return input, true
}

// Same Unicode rule as the accounting boundary; encoding/json intentionally
// replaces lone surrogates, which cannot be allowed for opaque identities.
func validUnicodeEscapes(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		i++
		if body[i] != 'u' {
			continue
		}
		value, _ := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
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

func bindAccountRequests(app core.App, service *ledger.Service) {
	submit := func(e *core.RecordRequestEvent) error {
		input, ok := e.Request.Context().Value(nativeInputKey{}).(nativeInput)
		if !ok || e.Auth == nil || !e.Auth.IsSuperuser() {
			own(e.Request, failuretype.PolicyRefused)
			return e.JSON(403, map[string]string{"code": "OPERATOR_NATIVE_INPUT_REFUSED", "stage": "native_account_write", "constraint": "validated_input_and_native_authority", "outcome": "no_effect", "next_step": "Use the restricted native editor with an authenticated operator session."})
		}
		if o := requestOwner(e.Request); o != nil {
			o.stage = failuretype.NativeWriteUnknown
		}
		var raw []byte
		var err error
		ctx, report := ledger.WithFailureReport(e.Request.Context())
		if e.Request.Method == http.MethodPost {
			raw, err = service.NativeAccountCreate(ctx, input.Create)
		} else {
			raw, err = service.NativeAccountUpdate(ctx, e.Request.PathValue("id"), input.Patch)
		}
		if o := requestOwner(e.Request); o != nil {
			o.runReturned(e.Request, report, err)
		} else {
			ledger.ObserveRunFailures(nil, report, err, failuretype.NativeWriteUnknown)
		}
		if err != nil {
			d := ledger.CanonicalFailure(err, failuretype.NativeWriteUnknown)
			status := 503
			switch d.Code {
			case ledger.CodeInput:
				status = 400
			case ledger.CodeAccount:
				status = 404
			case ledger.CodeNativeHold:
				status = 409
			}
			// Native-compatible envelope; never erase commit uncertainty.
			return e.JSON(status, struct {
				*ledger.Diagnostic
				Status  int               `json:"status"`
				Message string            `json:"message"`
				Data    map[string]string `json:"data"`
			}{d, status, "Synthetic native account write unavailable. Inspect code, constraint and outcome; no automatic retry.", map[string]string{}})
		}
		e.Response.Header().Set("Content-Type", "application/json")
		e.Response.WriteHeader(http.StatusOK)
		_, err = e.Response.Write(raw)
		return err // enclosing boundary aborts failed written delivery, never resubmits
	}
	// Do NOT call e.Next: that executes stock form.Submit with a different writer.
	app.OnRecordCreateRequest(ledger.AccountCollectionName).BindFunc(submit)
	app.OnRecordUpdateRequest(ledger.AccountCollectionName).BindFunc(submit)
}

func withNativeInput(req *http.Request, input nativeInput) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), nativeInputKey{}, input))
}
