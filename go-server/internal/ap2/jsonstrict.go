package ap2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// fieldSpec is one allowed JSON object field. A nil Fields map means a scalar.
type fieldSpec struct {
	Fields map[string]fieldSpec
}

func scalar() fieldSpec { return fieldSpec{} }

var (
	intentPayloadFields = map[string]fieldSpec{
		"signer":          scalar(),
		"budget_amount":   scalar(),
		"budget_currency": scalar(),
		"ttl_seconds":     scalar(),
		"issued_at":       scalar(),
		"description":     scalar(),
	}
	cartPayloadFields = map[string]fieldSpec{
		"signer":      scalar(),
		"merchant":    scalar(),
		"amount":      scalar(),
		"currency":    scalar(),
		"items_hash":  scalar(),
		"ttl_seconds": scalar(),
		"issued_at":   scalar(),
	}
	paymentPayloadFields = map[string]fieldSpec{
		"signer":    scalar(),
		"amount":    scalar(),
		"currency":  scalar(),
		"recipient": scalar(),
		"nonce":     scalar(),
	}
	authorizationFields = map[string]fieldSpec{
		"token":        scalar(),
		"from":         scalar(),
		"to":           scalar(),
		"value":        scalar(),
		"valid_after":  scalar(),
		"valid_before": scalar(),
		"nonce":        scalar(),
		"v":            scalar(),
		"r":            scalar(),
		"s":            scalar(),
	}
)

func payloadFields(mandateType string) (map[string]fieldSpec, bool) {
	switch MandateType(mandateType) {
	case MandateTypeIntent:
		return intentPayloadFields, true
	case MandateTypeCart:
		return cartPayloadFields, true
	case MandateTypePayment:
		return paymentPayloadFields, true
	default:
		return nil, false
	}
}

// strictObject rejects duplicate keys, case-variant keys, unknown keys, and
// trailing data, then returns. The caller decodes the same bytes afterwards.
func strictObject(raw []byte, fields map[string]fieldSpec) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := consumeSchemaObject(dec, fields, "json"); err != nil {
		return err
	}
	return requireEOF(dec)
}

func decodeFundRequest(raw []byte) (FundViaMandateRequest, error) {
	var req FundViaMandateRequest
	if err := strictFundBody(raw); err != nil {
		return req, err
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, err
	}
	return req, nil
}

func decodeValidateRequest(raw []byte) (ValidateMandateRequest, error) {
	var req ValidateMandateRequest
	if err := strictValidateBody(raw); err != nil {
		return req, err
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, err
	}
	return req, nil
}

func strictFundBody(raw []byte) error {
	return strictRequest(raw, map[string]bool{"escrow_id": true, "mandate_envelope": true})
}

func strictValidateBody(raw []byte) error {
	return strictRequest(raw, map[string]bool{"mandate_envelope": true})
}

// strictRequest walks one request object with json.Decoder.Token. The mandate
// envelope, including authorization and the typed payload, is checked here so
// encoding/json never sees duplicate or case-variant keys.
func strictRequest(raw []byte, top map[string]bool) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return errors.New("expected a JSON object")
	}
	seen := map[string]struct{}{}
	for dec.More() {
		key, err := objectKey(dec)
		if err != nil {
			return err
		}
		if err := noteKey(seen, top, key); err != nil {
			return err
		}
		switch key {
		case "mandate_envelope":
			if err := consumeMandateEnvelope(dec); err != nil {
				return err
			}
		case "escrow_id":
			if _, err := consumeScalar(dec, key); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown field %q", key)
		}
	}
	if err := endObject(dec); err != nil {
		return err
	}
	return requireEOF(dec)
}

func consumeMandateEnvelope(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return errors.New("mandate_envelope must be a JSON object")
	}
	allowed := map[string]bool{
		"type": true, "payload": true, "signature": true,
		"signer_address": true, "authorization": true,
	}
	seen := map[string]struct{}{}
	var mandateType string
	var payloadKeys []string
	for dec.More() {
		key, err := objectKey(dec)
		if err != nil {
			return err
		}
		if err := noteKey(seen, allowed, key); err != nil {
			return err
		}
		switch key {
		case "type":
			val, err := consumeScalar(dec, key)
			if err != nil {
				return err
			}
			s, ok := val.(string)
			if !ok {
				return errors.New("mandate type must be a string")
			}
			mandateType = s
		case "payload":
			keys, err := consumeOpenObject(dec)
			if err != nil {
				return fmt.Errorf("payload: %w", err)
			}
			payloadKeys = keys
		case "authorization":
			if err := consumeSchemaObject(dec, authorizationFields, "authorization"); err != nil {
				return err
			}
		default:
			if _, err := consumeScalar(dec, key); err != nil {
				return err
			}
		}
	}
	if err := endObject(dec); err != nil {
		return err
	}
	if payloadKeys == nil {
		return nil
	}
	fields, ok := payloadFields(mandateType)
	if !ok {
		// The service reports an unknown mandate type. Still reject keys that
		// are not part of any mandate payload.
		fields = map[string]fieldSpec{}
		for _, set := range []map[string]fieldSpec{intentPayloadFields, cartPayloadFields, paymentPayloadFields} {
			for name, spec := range set {
				fields[name] = spec
			}
		}
	}
	for _, key := range payloadKeys {
		if _, known := fields[key]; known {
			continue
		}
		if foldAllowed(fields, key) != "" {
			return fmt.Errorf("case-variant key %q", key)
		}
		return fmt.Errorf("unknown field %q", key)
	}
	return nil
}

// consumeOpenObject reads an object whose schema is applied by the caller.
// Nested values must be scalars. Duplicate and case-variant keys are rejected.
func consumeOpenObject(dec *json.Decoder) ([]string, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("expected a JSON object")
	}
	seen := map[string]struct{}{}
	var keys []string
	for dec.More() {
		key, err := objectKey(dec)
		if err != nil {
			return nil, err
		}
		fold := strings.ToLower(key)
		if _, dup := seen[fold]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		seen[fold] = struct{}{}
		keys = append(keys, key)
		if _, err := consumeScalar(dec, key); err != nil {
			return nil, err
		}
	}
	if err := endObject(dec); err != nil {
		return nil, err
	}
	return keys, nil
}

func consumeSchemaObject(dec *json.Decoder, fields map[string]fieldSpec, where string) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("invalid %s: %w", where, err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("invalid %s: expected a JSON object", where)
	}
	allowed := make(map[string]bool, len(fields))
	for name := range fields {
		allowed[name] = true
	}
	seen := map[string]struct{}{}
	for dec.More() {
		key, err := objectKey(dec)
		if err != nil {
			return err
		}
		if err := noteKey(seen, allowed, key); err != nil {
			return err
		}
		spec := fields[key]
		if spec.Fields != nil {
			if err := consumeSchemaObject(dec, spec.Fields, key); err != nil {
				return err
			}
			continue
		}
		if _, err := consumeScalar(dec, key); err != nil {
			return err
		}
	}
	return endObject(dec)
}

func objectKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", errors.New("expected a string object key")
	}
	return key, nil
}

func noteKey(seen map[string]struct{}, allowed map[string]bool, key string) error {
	fold := strings.ToLower(key)
	if _, dup := seen[fold]; dup {
		return fmt.Errorf("duplicate key %q", key)
	}
	seen[fold] = struct{}{}
	if allowed[key] {
		return nil
	}
	for name := range allowed {
		if strings.EqualFold(name, key) {
			return fmt.Errorf("case-variant key %q", key)
		}
	}
	return fmt.Errorf("unknown field %q", key)
}

func foldAllowed(fields map[string]fieldSpec, key string) string {
	for name := range fields {
		if strings.EqualFold(name, key) {
			return name
		}
	}
	return ""
}

func consumeScalar(dec *json.Decoder, key string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); ok {
		return nil, fmt.Errorf("unexpected nested value for %q (%s)", key, string(delim))
	}
	return tok, nil
}

func endObject(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '}' {
		return errors.New("expected end of object")
	}
	return nil
}

func requireEOF(dec *json.Decoder) error {
	_, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected trailing data")
}
