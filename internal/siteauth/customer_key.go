package siteauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const CustomerCollection = ledger.AccountCollectionName
const CustomerCollectionID = ledger.AccountCollectionID

// keyMaterial is request-private. Neither password nor raw key belongs in a
// record field other than the SDK password setter's transient input.
type keyMaterial struct{ lookup, password string }

func deriveKey(text string) (keyMaterial, error) {
	if len(text) < 43 || len(text) > 128 {
		return keyMaterial{}, keyInputFault()
	}
	for _, c := range []byte(text) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return keyMaterial{}, keyInputFault()
		}
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil || len(decoded) < 32 {
		return keyMaterial{}, keyInputFault()
	}
	lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + text))
	password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + text))
	return keyMaterial{"v1_" + hex.EncodeToString(lookup[:]), base64.RawURLEncoding.EncodeToString(password[:])}, nil
}

func keyInputFault() *ledger.Diagnostic {
	d := refused("SITE_KEY_INPUT", "customer_key_prepare", "gateway_key_format", failuretype.RequestInvalid)
	d.Outcome = "no_effect"
	d.NextStep = "Use a privately generated gateway-format key in both password fields; the SDK creates the account identity. No credential was saved by this preparation step; do not share input values."
	return d
}

// CustomerKeyMiddleware adapts only the explicitly selected stock superuser
// record-editor recipe. It adds no authority gate; other SDK operations pass
// through unchanged. Run after SDK auth/body limits, before record form loading:
// changing the record password later can skip SDK confirmation validation.
func CustomerKeyMiddleware() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{Id: "intakeCustomerKeyInput", Priority: apis.DefaultBodyLimitMiddlewarePriority + 1, Func: prepareCustomerKey}
}

func prepareCustomerKey(e *core.RequestEvent) (result error) {
	// Catch preparation only, not downstream SDK writes whose outcome may differ.
	if err := prepareCustomerInput(e); err != nil {
		return err
	}
	return e.Next()
}

func prepareCustomerInput(e *core.RequestEvent) (result error) {
	defer func() {
		if recover() != nil {
			result = keyInputUnknown()
		}
	}()
	selector := e.Request.PathValue("collection")
	if selector != CustomerCollection && selector != CustomerCollectionID || e.Auth == nil || !e.Auth.IsSuperuser() {
		return nil
	}
	prefix := "/api/collections/" + selector + "/records"
	if !(e.Request.Method == http.MethodPost && e.Request.URL.Path == prefix || e.Request.Method == http.MethodPatch && e.Request.URL.Path == prefix+"/"+e.Request.PathValue("id")) {
		return nil
	}
	info, err := e.RequestInfo()
	if err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return err
		}
		var syntax *jsontext.SyntacticError
		var semantic *json.SemanticError
		if errors.As(err, &syntax) || errors.As(err, &semantic) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, router.ErrUnsupportedContentType) {
			return keyInputFault()
		}
		return classified(keyInputUnknown(), err)
	}
	if e.Request.Method == http.MethodPost {
		if _, supplied := info.Body["balance"]; !supplied {
			info.Body["balance"] = "0"
		}
		if _, supplied := info.Body["enabled"]; !supplied {
			info.Body["enabled"] = true
		}
	}
	value, supplied := info.Body["password"]
	if !supplied {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return keyInputFault()
	}
	if text == "" {
		return nil // Leave ordinary SDK required/confirmation validation intact.
	}
	material, err := deriveKey(text)
	if err != nil {
		return err
	}
	confirmation := ""
	if value, supplied := info.Body["passwordConfirm"]; supplied {
		text, ok := value.(string)
		if !ok {
			return keyInputFault()
		}
		if text != "" {
			other, err := deriveKey(text)
			if err != nil {
				return err
			}
			confirmation = other.password
		}
	}
	info.Body["password"] = material.password
	info.Body["passwordConfirm"] = confirmation
	info.Body["key_lookup"] = material.lookup
	return nil
}

func keyInputUnknown() *ledger.Diagnostic {
	d := refused("SITE_KEY_UNAVAILABLE", "customer_key_prepare", "preparation_cause_unknown", failuretype.NativeAuthUnknown)
	d.Outcome = "no_effect"
	d.NextStep = "Key preparation did not complete; underlying cause unknown. Inspect the native form privately before another attempt. Do not share credentials."
	return d
}
