package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
)

var (
	errNotObject = errors.New("not one object")
	errDuplicate = errors.New("duplicate key")
	errTrailing  = errors.New("trailing data")
	errNull      = errors.New("null required field")
)

func bytesReader(body []byte) io.Reader { return bytes.NewReader(body) }

// bindFields maps the decoded top-level object onto target's exported fields
// using their json tags. Extra keys are ignored for additive compatibility;
// nulls and (when required) absent known fields are rejected. target must be a pointer to a struct with only string, *int64
// or int64 fields; the closed request structs in handler.go satisfy this.
func bindFields(fields map[string]json.RawMessage, target any, requireAll bool) error {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return errNotObject
	}
	structure := value.Elem()
	typ := structure.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := field.Tag.Get("json")
		if name == "" {
			continue
		}
		raw, present := fields[name]
		if !present {
			if requireAll {
				return errNull
			}
			continue
		}
		if isNull(raw) || !validUnicodeEscapes(raw) {
			return errNull
		}
		slot := structure.Field(i)
		switch slot.Kind() {
		case reflect.String:
			var decoded string
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return err
			}
			slot.SetString(decoded)
		case reflect.Pointer:
			inner := reflect.New(slot.Type().Elem())
			if err := json.Unmarshal(raw, inner.Interface()); err != nil {
				return err
			}
			slot.Set(inner)
		default:
			if err := json.Unmarshal(raw, slot.Addr().Interface()); err != nil {
				return err
			}
		}
	}
	return nil
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return bytes.Equal(trimmed, []byte("null"))
}
