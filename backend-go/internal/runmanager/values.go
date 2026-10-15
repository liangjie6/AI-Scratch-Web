package runmanager

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// pythonInt converts a JSON value the way an isinstance(value, int) guard
// followed by integer use would. Go maps produced by encoding/json hold
// float64 for every number, so integral floats are accepted as well.
func pythonInt(value any) (int, bool) {
	switch typed := value.(type) {
	case bool:
		return 0, false
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != math.Trunc(typed) {
			return 0, false
		}
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

// jsonInt reads an int from a decoded JSON value.
func jsonInt(value any) (int64, bool) { return jsonx.Int(value) }

// pyStr mirrors Python's str() for the values that can appear in an event
// payload: None renders as "None", booleans as True/False, numbers verbatim.
func pyStr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

// pythonEqual mirrors Python equality for JSON scalars.
func pythonEqual(left, right any) bool {
	leftNumber, leftIsNumber := pythonFloat(left)
	rightNumber, rightIsNumber := pythonFloat(right)
	if leftIsNumber && rightIsNumber {
		return leftNumber == rightNumber
	}
	return reflect.DeepEqual(left, right)
}

func pythonFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case bool:
		return 0, false
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
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

// pythonContains mirrors Python membership for the containers the run manager
// builds: lists compare with ==, text behaves like a substring test.
func pythonContains(container any, needle any) bool {
	switch typed := container.(type) {
	case []any:
		for _, item := range typed {
			if pythonEqual(item, needle) {
				return true
			}
		}
		return false
	case []string:
		text, ok := needle.(string)
		if !ok {
			return false
		}
		for _, item := range typed {
			if item == text {
				return true
			}
		}
		return false
	case string:
		text, ok := needle.(string)
		return ok && strings.Contains(typed, text)
	default:
		return false
	}
}

// jsonObjectValue returns a JSON object member.
func jsonObjectValue(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// nestedInt reads a member of a nested JSON object with the Python default
// semantics of dict.get(key, {}).get(key, -1).
func nestedInt(summary map[string]any, objectKey, valueKey string, fallback int) (int, bool) {
	object, ok := jsonObjectValue(summary[objectKey])
	if !ok {
		return fallback, false
	}
	value, exists := object[valueKey]
	if !exists {
		return fallback, false
	}
	parsed, ok := pythonInt(value)
	if !ok {
		return fallback, false
	}
	return parsed, true
}

// jsonArray returns a JSON array member.
func jsonArray(value any) ([]any, bool) {
	items, ok := value.([]any)
	return items, ok
}

// copyMap ports Python's dict(mapping) shallow copy.
func copyMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// stringSliceEqual treats a nil slice and an empty slice as equal, matching
// pydantic list defaults on both sides of a Python comparison.
func stringSliceEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// equalTargetContext mirrors pydantic equality for TargetContext, where a
// missing list field decodes to the same empty list as an explicit [].
func equalTargetContext(left, right schema.TargetContext) bool {
	return left.Name == right.Name &&
		left.IsStage == right.IsStage &&
		left.HasScripts == right.HasScripts &&
		left.Mode == right.Mode &&
		stringSliceEqual(left.Costumes, right.Costumes) &&
		stringSliceEqual(left.Backdrops, right.Backdrops) &&
		stringSliceEqual(left.Sounds, right.Sounds) &&
		stringSliceEqual(left.SpriteNames, right.SpriteNames) &&
		left.CurrentProgram == right.CurrentProgram
}
