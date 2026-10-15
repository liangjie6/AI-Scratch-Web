// Package jsonx provides JSON helpers with the same value semantics as the
// Python reference implementation (json.loads / json.dumps with sort_keys and
// compact separators).
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Parse decodes JSON the way Python's json.loads does: integers become int64,
// other numbers become float64, objects become map[string]any.
func Parse(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	return normalize(raw), nil
}

// ParseObject decodes a JSON object.
func ParseObject(data []byte) (map[string]any, error) {
	value, err := Parse(data)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value is not a JSON object")
	}
	return object, nil
}

func normalize(value any) any {
	switch typed := value.(type) {
	case json.Number:
		text := typed.String()
		if !strings.ContainsAny(text, ".eE") {
			if parsed, err := typed.Int64(); err == nil {
				return parsed
			}
		}
		parsed, err := typed.Float64()
		if err != nil {
			return text
		}
		return parsed
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalize(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = normalize(item)
		}
		return typed
	default:
		return value
	}
}

// Marshal encodes a value with compact separators and without HTML escaping.
func Marshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// Canonical encodes a value the way the Python reference does for hashing:
// sorted object keys, compact separators and no HTML escaping.
func Canonical(value any) (string, error) {
	encoded, err := Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// FormatFloat renders a float64 using Python's repr rules closely enough for
// canonical payloads produced by this service.
func FormatFloat(value float64) string {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "null"
	}
	if value == math.Trunc(value) && math.Abs(value) < 1e16 {
		return strconv.FormatFloat(value, 'f', 1, 64)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// Int converts a JSON number to an int64 when it is integral.
func Int(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		if typed == math.Trunc(typed) {
			return int64(typed), true
		}
		return 0, false
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// Float converts a JSON number to a float64.
func Float(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// String converts a JSON value to a string.
func String(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

// Bool converts a JSON value to a bool.
func Bool(value any) (bool, bool) {
	flag, ok := value.(bool)
	return flag, ok
}

// SortedKeys returns the object keys in sorted order.
func SortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
