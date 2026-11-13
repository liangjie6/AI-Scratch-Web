package authoring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// CheckJSON bounds the token stream before recursively decoding any model-owned
// tree. Duplicate object keys and trailing values are always rejected.
func CheckJSON(raw []byte, limits Limits) error {
	l := limits.defaults()
	if len(raw) == 0 || len(raw) > l.MaxBytes {
		return issue("RESPONSE_SIZE", "/", "JSON response is empty or exceeds the byte budget")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	count := 0
	var visit func(int, string) error
	visit = func(depth int, path string) error {
		if depth > l.MaxJSONDepth {
			return issue("JSON_DEPTH", path, "JSON nesting exceeds the budget")
		}
		count++
		if count > l.MaxJSONValues {
			return issue("JSON_BUDGET", path, "JSON value count exceeds the budget")
		}
		token, err := d.Token()
		if err != nil {
			return issue("JSON_SYNTAX", path, err.Error())
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return issue("JSON_SYNTAX", path, err.Error())
				}
				key, ok := keyToken.(string)
				if !ok {
					return issue("JSON_SYNTAX", path, "object key must be a string")
				}
				child := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
				if seen[key] {
					return issue("DUPLICATE_KEY", child, "JSON object keys must be unique")
				}
				seen[key] = true
				if len(key) > 2000 {
					return issue("STRING_SIZE", child, "JSON key is too long")
				}
				if err := visit(depth+1, child); err != nil {
					return err
				}
			}
		case '[':
			index := 0
			for d.More() {
				if err := visit(depth+1, fmt.Sprintf("%s/%d", path, index)); err != nil {
					return err
				}
				index++
			}
		default:
			return issue("JSON_SYNTAX", path, "unexpected closing delimiter")
		}
		_, err = d.Token()
		if err != nil {
			return issue("JSON_SYNTAX", path, err.Error())
		}
		return nil
	}
	if err := visit(0, ""); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return issue("JSON_TRAILING", "/", "exactly one JSON value is required")
	}
	return nil
}

func decodeBounded(raw []byte, target any, l Limits) error {
	if err := CheckJSON(raw, l); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return issue("SHAPE", "/", err.Error())
	}
	return nil
}

func ParseIntent(raw []byte, limits Limits) (*IntentSpec, error) {
	var spec IntentSpec
	if err := decodeBounded(raw, &spec, limits); err != nil {
		return nil, err
	}
	// The source route pins IntentSpec v1. A missing redundant version may be
	// supplied locally; an explicitly different (including null/zero) version
	// still fails closed. Frozen artifacts always retain the explicit version.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, issue("SHAPE", "/", "IntentSpec must be an object")
	}
	if _, present := fields["schema_version"]; !present {
		spec.SchemaVersion = IntentVersion
	}
	if spec.SchemaVersion != IntentVersion {
		return nil, issue("VERSION", "/schema_version", "IntentSpec schema_version must be 1")
	}
	return &spec, nil
}

func ParseCompact(raw []byte, limits Limits) (*SlotResponse, error) {
	var response SlotResponse
	if err := decodeBounded(raw, &response, limits); err != nil {
		return nil, err
	}
	if response.SlotID == "" {
		return nil, issue("SLOT_UNKNOWN", "/slot_id", "slot_id is required")
	}
	switch response.Status {
	case "ready":
		if response.Body == nil || response.Revision != nil {
			return nil, issue("SHAPE", "/", "ready requires an array body and null revision")
		}
	case "needs_revision":
		if response.Body != nil || response.Revision == nil {
			return nil, issue("SHAPE", "/", "needs_revision requires null body and a revision")
		}
		r := response.Revision
		if r.Reason != "missing_capability" && r.Reason != "missing_interface" && r.Reason != "unsupported" {
			return nil, issue("SHAPE", "/revision/reason", "unknown revision reason")
		}
		if len(r.RequestedOpcodes) > 24 || len(r.Explanation) == 0 || len(r.Explanation) > 2000 {
			return nil, issue("SHAPE", "/revision", "revision exceeds its budget")
		}
		for _, op := range r.RequestedOpcodes {
			if len(op) == 0 || len(op) > 120 {
				return nil, issue("SHAPE", "/revision/requested_opcodes", "invalid opcode name")
			}
		}
	default:
		return nil, issue("SHAPE", "/status", "status must be ready or needs_revision")
	}
	return &response, nil
}
