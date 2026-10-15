package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ai-scratch-web/backend/internal/jsonx"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// isPythonSpace reports whether r is stripped by Python's str.strip().
func isPythonSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// StripPythonSpace ports Python's str.strip().
func StripPythonSpace(value string) string {
	return strings.TrimFunc(value, isPythonSpace)
}

// NormalizeContractText ports normalize_contract_text.
func NormalizeContractText(value string) (string, error) {
	normalized := StripPythonSpace(norm.NFKC.String(value))
	for _, char := range normalized {
		if char < 32 || char == 127 {
			return "", errors.New("strings cannot contain control characters")
		}
	}
	if normalized == "" {
		return "", errors.New("string must not be empty after normalization")
	}
	return normalized, nil
}

// LogicalKey ports logical_key.
func LogicalKey(value string) (string, error) {
	normalized, err := NormalizeContractText(value)
	if err != nil {
		return "", err
	}
	return cases.Fold().String(normalized), nil
}

// PythonRepr renders a JSON-ish value the way Python's str()/repr() does.
func PythonRepr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return jsonx.FormatFloat(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, PythonRepr(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := jsonx.SortedKeys(typed)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, "'"+key+"': "+PythonRepr(typed[key]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", typed)
	}
}

var jsonPointerPartPattern = regexp.MustCompile("[^.[\\]/]+")

// ToJSONPointer ports to_json_pointer. path may be a string path or a slice of
// path components (the Python tuple form).
func ToJSONPointer(path any) string {
	var parts []string
	switch typed := path.(type) {
	case string:
		value := StripPythonSpace(typed)
		if value == "" || value == "$" {
			return ""
		}
		if strings.HasPrefix(value, "/") {
			return value
		}
		if strings.HasPrefix(value, "$") {
			value = value[1:]
		}
		parts = jsonPointerPartPattern.FindAllString(value, -1)
	case []string:
		parts = typed
	case []any:
		parts = make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, PythonRepr(item))
		}
	default:
		return ""
	}
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(part, "~", "~0")
		part = strings.ReplaceAll(part, "/", "~1")
		escaped = append(escaped, part)
	}
	if len(escaped) == 0 {
		return ""
	}
	return "/" + strings.Join(escaped, "/")
}

const pythonSpaceClass = `\t\n\v\f\r \x{001C}-\x{001F}\x{0085}\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`

var jsonFencePattern = regexp.MustCompile(
	"(?is)^[" + pythonSpaceClass + "]*" + "```" + "(?:json)?[" + pythonSpaceClass + "]*\n(.*?)\n" + "```" + "[" + pythonSpaceClass + "]*$",
)

// ExtractSingleJSON ports extract_single_json.
func ExtractSingleJSON(text string) (any, error) {
	stripped := StripPythonSpace(text)
	if match := jsonFencePattern.FindStringSubmatch(stripped); match != nil {
		stripped = StripPythonSpace(match[1])
	} else if strings.Contains(stripped, "```") {
		return nil, errors.New("invalid JSON wrapper")
	}
	decoder := json.NewDecoder(strings.NewReader(stripped))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, jsonDecodeValueError(err, stripped)
	}
	end := decoder.InputOffset()
	if StripPythonSpace(stripped[end:]) != "" {
		return nil, errors.New("trailing content after JSON")
	}
	return numbersToPython(value), nil
}

func jsonDecodeValueError(err error, input string) error {
	message := err.Error()
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		message = "Expecting value"
	}
	offset := int64(len(input))
	if syntax, ok := err.(*json.SyntaxError); ok {
		offset = syntax.Offset
		message = syntax.Error()
	}
	line := 1
	column := int(offset)
	if offset > int64(len(input)) {
		offset = int64(len(input))
		column = int(offset)
	}
	if offset > 0 {
		prefix := input[:offset]
		if index := strings.LastIndex(prefix, "\n"); index >= 0 {
			line = strings.Count(prefix, "\n") + 1
			column = int(offset) - index
		}
	}
	return fmt.Errorf("invalid JSON response at line %d column %d: %s", line, column, message)
}

func numbersToPython(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if parsed, ok := jsonx.Int(typed); ok {
			return parsed
		}
		if parsed, ok := jsonx.Float(typed); ok {
			return parsed
		}
		return typed.String()
	case map[string]any:
		for key, item := range typed {
			typed[key] = numbersToPython(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = numbersToPython(item)
		}
		return typed
	default:
		return value
	}
}

// CanonicalContractJSON ports canonical_contract_json: it reproduces Python's
// json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',',':'))
// after model_dump(mode='json', exclude_none=True) and the integral-float
// normalization used for browser-compatible hashing.
func CanonicalContractJSON(value any) (string, error) {
	payload, err := canonicalPayload(value)
	if err != nil {
		return "", err
	}
	return jsonx.Canonical(payload)
}

// ContractSHA256 ports contract_sha256.
func ContractSHA256(value any) (string, error) {
	canonical, err := CanonicalContractJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

func canonicalPayload(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
	}
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	tree, err := jsonx.Parse(encoded)
	if err != nil {
		return nil, err
	}
	if rv.Kind() == reflect.Struct {
		tree = canonicalStruct(rv, tree)
	}
	return normalizeIntegralNumbers(tree), nil
}

// canonicalStruct mirrors model_dump(mode='json', exclude_none=True): nil
// optional fields are dropped and nil list/dict fields become empty containers.
func canonicalStruct(value reflect.Value, node any) any {
	object, ok := node.(map[string]any)
	if !ok {
		return node
	}
	for _, field := range structJSONFields(value.Type()) {
		fieldValue := value.Field(field.index)
		switch fieldValue.Kind() {
		case reflect.Ptr, reflect.Interface:
			if fieldValue.IsNil() {
				delete(object, field.name)
				continue
			}
		case reflect.Slice:
			if fieldValue.IsNil() {
				if value.Type().Field(field.index).Tag.Get("optional") == "true" {
					delete(object, field.name)
				} else {
					object[field.name] = []any{}
				}
				continue
			}
		case reflect.Map:
			if fieldValue.IsNil() {
				object[field.name] = map[string]any{}
				continue
			}
		}
		child, present := object[field.name]
		if !present {
			continue
		}
		object[field.name] = canonicalField(fieldValue, child)
	}
	return object
}

func canonicalField(fieldValue reflect.Value, node any) any {
	for fieldValue.Kind() == reflect.Ptr || fieldValue.Kind() == reflect.Interface {
		if fieldValue.IsNil() {
			return nil
		}
		fieldValue = fieldValue.Elem()
	}
	switch fieldValue.Kind() {
	case reflect.Struct:
		return canonicalStruct(fieldValue, node)
	case reflect.Slice, reflect.Array:
		items, ok := node.([]any)
		if !ok {
			return node
		}
		for index := 0; index < fieldValue.Len() && index < len(items); index++ {
			items[index] = canonicalField(fieldValue.Index(index), items[index])
		}
		return items
	default:
		return node
	}
}

// normalizeIntegralNumbers mirrors the browser-compatibility normalization of
// canonical_contract_json: integral floats collapse to integers.
func normalizeIntegralNumbers(value any) any {
	switch typed := value.(type) {
	case float64:
		if !math.IsInf(typed, 0) && !math.IsNaN(typed) && typed == math.Trunc(typed) &&
			typed >= -9223372036854775808.0 && typed < 9223372036854775808.0 {
			return int64(typed)
		}
		return typed
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		if parsed, ok := jsonx.Int(typed); ok {
			return parsed
		}
		if parsed, ok := jsonx.Float(typed); ok {
			return parsed
		}
		return typed.String()
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeIntegralNumbers(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = normalizeIntegralNumbers(item)
		}
		return typed
	default:
		return value
	}
}

// sortedStringSet is a helper for deterministic iteration over name sets.
func sortedStringSet(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
