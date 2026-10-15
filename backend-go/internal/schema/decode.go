package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

// requiredJSONFieldsProvider is implemented by every ported model. It lists the
// pydantic fields that have no default and therefore must be present in the
// input object.
type requiredJSONFieldsProvider interface {
	requiredJSONFields() []string
}

// modelValidator is implemented by every ported model.
type modelValidator interface {
	Validate() error
}

// DecodeStrict decodes raw into target with pydantic StrictModel semantics:
// the top-level value must be a JSON object, unknown fields are rejected at
// every nesting level, fields without a Python default must be present, absent
// fields receive their Python defaults, and the model validator runs last.
func DecodeStrict(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return &SchemaValidationError{Message: "Input should be a valid dictionary"}
	}
	if err := strictUnmarshal(raw, target); err != nil {
		return err
	}
	tree, err := jsonx.Parse(raw)
	if err != nil {
		return err
	}
	object, ok := tree.(map[string]any)
	if !ok {
		return &SchemaValidationError{Message: "Input should be a valid dictionary"}
	}
	if err := applyModelSchema(object, reflect.ValueOf(target), ""); err != nil {
		return err
	}
	if validator, ok := target.(modelValidator); ok {
		return validator.Validate()
	}
	return nil
}

// strictUnmarshal decodes one JSON value, rejecting unknown fields and any
// trailing content.
func strictUnmarshal(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &SchemaValidationError{Message: "trailing content after JSON value"}
		}
		return err
	}
	return nil
}

// jsonObjectKeys returns the object keys in JSON document order.
func jsonObjectKeys(data []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("value is not a JSON object")
	}
	var keys []string
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyToken.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func sortStrings(values []string) {
	sort.Strings(values)
}

type structJSONField struct {
	name  string
	index int
}

func structJSONFields(structType reflect.Type) []structJSONField {
	fields := make([]structJSONField, 0, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name, skip := jsonFieldName(field)
		if skip {
			continue
		}
		fields = append(fields, structJSONField{name: name, index: index})
	}
	return fields
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name, false
	}
	name := tag
	if comma := strings.Index(tag, ","); comma >= 0 {
		name = tag[:comma]
	}
	if name == "-" {
		return "", true
	}
	if name == "" {
		return field.Name, false
	}
	return name, false
}

func joinJSONPath(base, name string) string {
	return base + "/" + name
}

// applyModelSchema walks the decoded value together with the parsed JSON tree.
// It enforces field presence, Python field defaults and pydantic's rejection of
// null for non-optional fields, recursing into nested models.
func applyModelSchema(node any, value reflect.Value, path string) error {
	for value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch typed := node.(type) {
	case map[string]any:
		switch value.Kind() {
		case reflect.Struct:
			return applyModelSchemaStruct(typed, value, path)
		case reflect.Map:
			for _, key := range jsonx.SortedKeys(typed) {
				item := value.MapIndex(reflect.ValueOf(key))
				if !item.IsValid() {
					continue
				}
				if err := applyModelSchema(typed[key], item, joinJSONPath(path, key)); err != nil {
					return err
				}
			}
		}
	case []any:
		switch value.Kind() {
		case reflect.Slice, reflect.Array:
			for index := 0; index < value.Len() && index < len(typed); index++ {
				if err := applyModelSchema(typed[index], value.Index(index), fmt.Sprintf("%s/%d", path, index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// jsonFieldsRecorder is implemented by models that need to know which fields
// the incoming JSON object actually contained (pydantic model_fields_set).
type jsonFieldsRecorder interface {
	recordJSONFields(object map[string]any)
}

func applyModelSchemaStruct(object map[string]any, value reflect.Value, path string) error {
	if value.CanAddr() {
		if recorder, ok := value.Addr().Interface().(jsonFieldsRecorder); ok {
			recorder.recordJSONFields(object)
		}
		if provider, ok := value.Addr().Interface().(requiredJSONFieldsProvider); ok {
			for _, name := range provider.requiredJSONFields() {
				if _, present := object[name]; !present {
					return &SchemaValidationError{Path: joinJSONPath(path, name), Message: "Field required"}
				}
			}
		}
	}
	for _, field := range structJSONFields(value.Type()) {
		fieldValue := value.Field(field.index)
		fieldPath := joinJSONPath(path, field.name)
		raw, present := object[field.name]
		if !present {
			if err := applyAbsentFieldDefault(fieldValue, value.Type().Field(field.index)); err != nil {
				return err
			}
			continue
		}
		if raw == nil {
			if err := rejectNull(fieldValue, value.Type().Field(field.index), fieldPath); err != nil {
				return err
			}
			continue
		}
		if err := applyModelSchema(raw, fieldValue, fieldPath); err != nil {
			return err
		}
	}
	return nil
}

func applyAbsentFieldDefault(fieldValue reflect.Value, field reflect.StructField) error {
	if !fieldValue.CanSet() {
		return nil
	}
	if raw, ok := field.Tag.Lookup("default"); ok {
		switch fieldValue.Kind() {
		case reflect.String:
			fieldValue.SetString(raw)
		case reflect.Bool:
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				return err
			}
			fieldValue.SetBool(parsed)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return err
			}
			fieldValue.SetInt(parsed)
		case reflect.Float32, reflect.Float64:
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return err
			}
			fieldValue.SetFloat(parsed)
		}
		return nil
	}
	switch fieldValue.Kind() {
	case reflect.Slice:
		// Python list fields with default_factory=list become []; Optional list
		// fields stay None (nil) so min_length constraints are skipped.
		if fieldValue.IsNil() && field.Tag.Get("optional") != "true" {
			fieldValue.Set(reflect.MakeSlice(fieldValue.Type(), 0, 0))
		}
	case reflect.Map:
		if fieldValue.IsNil() {
			fieldValue.Set(reflect.MakeMap(fieldValue.Type()))
		}
	case reflect.Struct:
		if fieldValue.CanAddr() {
			if setter, ok := fieldValue.Addr().Interface().(pythonDefaultSetter); ok {
				setter.setPythonDefault()
			}
		}
	}
	return nil
}

// pythonDefaultSetter is implemented by models whose Python default is a
// non-zero object (for example ThinkingSettings()).
type pythonDefaultSetter interface {
	setPythonDefault()
}

func rejectNull(fieldValue reflect.Value, field reflect.StructField, path string) error {
	if field.Tag.Get("optional") == "true" {
		return nil
	}
	switch fieldValue.Kind() {
	case reflect.Ptr, reflect.Interface:
		return nil
	case reflect.Bool:
		return &SchemaValidationError{Path: path, Message: "Input should be a valid boolean"}
	case reflect.String:
		return &SchemaValidationError{Path: path, Message: "Input should be a valid string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &SchemaValidationError{Path: path, Message: "Input should be a valid integer"}
	case reflect.Float32, reflect.Float64:
		return &SchemaValidationError{Path: path, Message: "Input should be a valid number"}
	case reflect.Slice, reflect.Array:
		return &SchemaValidationError{Path: path, Message: "Input should be a valid list"}
	default:
		return &SchemaValidationError{Path: path, Message: "Input should be a valid dictionary"}
	}
}

func validationError(path, message string) error {
	return &SchemaValidationError{Path: path, Message: message}
}

func runeLength(value string) int {
	return len([]rune(value))
}

func requireStringLength(path, value string, minLength, maxLength int) error {
	length := runeLength(value)
	if minLength >= 0 && length < minLength {
		return validationError(path, fmt.Sprintf("String should have at least %d characters", minLength))
	}
	if maxLength >= 0 && length > maxLength {
		return validationError(path, fmt.Sprintf("String should have at most %d characters", maxLength))
	}
	return nil
}

// validateShortString enforces ShortString (min_length=1, max_length=120).
func validateShortString(path, value string) error {
	return requireStringLength(path, value, 1, MaxShortStringLength)
}

// validateLongString enforces LongString (min_length=1, max_length=2000).
func validateLongString(path, value string) error {
	return requireStringLength(path, value, 1, MaxStringLength)
}

func validateOptionalShortString(path string, value *string) error {
	if value == nil {
		return nil
	}
	return validateShortString(path, *value)
}

func requireListLength(path string, length, minLength, maxLength int) error {
	if minLength >= 0 && length < minLength {
		return validationError(path, fmt.Sprintf("List should have at least %d items", minLength))
	}
	if maxLength >= 0 && length > maxLength {
		return validationError(path, fmt.Sprintf("List should have at most %d items", maxLength))
	}
	return nil
}

func requireIntRange(path string, value, minValue, maxValue int) error {
	if err := requireIntMin(path, value, minValue); err != nil {
		return err
	}
	return requireIntMax(path, value, maxValue)
}

func requireIntMin(path string, value, minValue int) error {
	if value < minValue {
		return validationError(path, fmt.Sprintf("Input should be greater than or equal to %d", minValue))
	}
	return nil
}

func requireIntMax(path string, value, maxValue int) error {
	if value > maxValue {
		return validationError(path, fmt.Sprintf("Input should be less than or equal to %d", maxValue))
	}
	return nil
}

func requireFloatRange(path string, value, minValue, maxValue float64) error {
	if err := requireFloatMin(path, value, minValue); err != nil {
		return err
	}
	return requireFloatMax(path, value, maxValue)
}

func requireFloatMin(path string, value, minValue float64) error {
	if value < minValue {
		return validationError(path, fmt.Sprintf("Input should be greater than or equal to %v", minValue))
	}
	return nil
}

func requireFloatMax(path string, value, maxValue float64) error {
	if value > maxValue {
		return validationError(path, fmt.Sprintf("Input should be less than or equal to %v", maxValue))
	}
	return nil
}

// validateLiteral ports Literal[...] validation.
func validateLiteral(path, value string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return validationError(path, "Input should be "+quoteOrList(allowed))
}

func quoteOrList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+value+"'")
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// validateScalarOrListValue enforces float | str | bool | list[str | float | bool]
// unions (with an optional None already handled by the caller).
func validateScalarOrListValue(path string, value any, allowList bool) error {
	switch typed := value.(type) {
	case string, bool, int64, int, float64:
		return nil
	case []any:
		if !allowList {
			return validationError(path, "Input should be a valid string, number or boolean")
		}
		for index, item := range typed {
			if err := validateScalarOrListValue(fmt.Sprintf("%s/%d", path, index), item, false); err != nil {
				return err
			}
		}
		return nil
	case nil:
		return nil
	default:
		if allowList {
			return validationError(path, "Input should be a valid string, number, boolean or list")
		}
		return validationError(path, "Input should be a valid string, number or boolean")
	}
}
