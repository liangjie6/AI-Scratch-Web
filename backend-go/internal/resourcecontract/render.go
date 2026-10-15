package resourcecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// IntPtr returns a pointer to v; it exists so generated data can express optional limits.
func IntPtr(v int) *int { return &v }

// ActiveOperationSpecs returns the operation specs permitted by the deployment capability.
func ActiveOperationSpecs(allowDestructiveResourceOps bool) []*OperationSpec {
	result := make([]*OperationSpec, 0, len(OperationSpecs))
	for i := range OperationSpecs {
		spec := &OperationSpecs[i]
		if allowDestructiveResourceOps || !spec.RequiresDestructiveApproval {
			result = append(result, spec)
		}
	}
	return result
}

func operationAsMap(spec *OperationSpec) map[string]any {
	defaults := map[string]any{}
	for _, item := range spec.Defaults {
		defaults[item.Key] = item.Value
	}
	toAny := func(values []string) []any {
		result := make([]any, 0, len(values))
		for _, value := range values {
			result = append(result, value)
		}
		return result
	}
	var minValue, maxValue any
	if spec.NestedCostumesMin != nil {
		minValue = *spec.NestedCostumesMin
	}
	if spec.NestedCostumesMax != nil {
		maxValue = *spec.NestedCostumesMax
	}
	return map[string]any{
		"op":                  spec.Name,
		"required_fields":     toAny(spec.RequiredFields),
		"optional_fields":     toAny(spec.OptionalFields),
		"defaults":            defaults,
		"forbidden_fields":    toAny(spec.ForbiddenFields),
		"target_kinds":        toAny(spec.TargetKinds),
		"source_asset_types":  toAny(spec.SourceAssetTypes),
		"source_kinds":        toAny(spec.SourceKinds),
		"nested_costumes_min": minValue,
		"nested_costumes_max": maxValue,
		"description":         spec.Description,
	}
}

// OperationMatrix returns the active resource-operation contract as ordered JSON objects.
func OperationMatrix(allowDestructiveResourceOps bool) []map[string]any {
	specs := ActiveOperationSpecs(allowDestructiveResourceOps)
	result := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		result = append(result, operationAsMap(spec))
	}
	return result
}

// MarshalCompact mirrors the Python reference encoding used inside prompts:
// sorted keys, no whitespace and no HTML escaping.
func MarshalCompact(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "")
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimRight(buffer.String(), "\n"), nil
}

// OperationContractJSON renders the active resource-operation matrix as canonical JSON.
func OperationContractJSON(allowDestructiveResourceOps bool) string {
	encoded, err := MarshalCompact(OperationMatrix(allowDestructiveResourceOps))
	if err != nil {
		return "[]"
	}
	return encoded
}

func sourceSpecsJSON() string {
	specs := make([]map[string]any, 0, len(SourceSpecs))
	for _, spec := range SourceSpecs {
		toAny := func(values []string) []any {
			result := make([]any, 0, len(values))
			for _, value := range values {
				result = append(result, value)
			}
			return result
		}
		specs = append(specs, map[string]any{
			"kind":            spec.Kind,
			"required_fields": toAny(spec.RequiredFields),
			"optional_fields": toAny(spec.OptionalFields),
			"asset_types":     toAny(spec.AssetTypes),
		})
	}
	encoded, err := MarshalCompact(specs)
	if err != nil {
		return "[]"
	}
	return encoded
}

func pythonRepr(value any) string {
	switch typed := value.(type) {
	case string:
		return "'" + typed + "'"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// RenderOperationContract renders the human-readable contract appended to model prompts.
func RenderOperationContract(allowDestructiveResourceOps bool) string {
	specs := ActiveOperationSpecs(allowDestructiveResourceOps)
	rows := []string{
		fmt.Sprintf("RESOURCE_OPERATION_CONTRACT (schema_version=%d; generated from backend/app/resource_contract.py):", ContractVersion),
		"Every resource operation must contain only required and optional fields. Never add a forbidden field.",
		"",
		"| op | required fields | optional/default fields | forbidden fields |",
		"| --- | --- | --- | --- |",
	}
	for _, spec := range specs {
		optional := "none"
		if len(spec.OptionalFields) > 0 {
			optional = strings.Join(spec.OptionalFields, ", ")
		}
		if len(spec.Defaults) > 0 {
			parts := make([]string, 0, len(spec.Defaults))
			for _, item := range spec.Defaults {
				parts = append(parts, fmt.Sprintf("%s=%s", item.Key, pythonRepr(item.Value)))
			}
			optional += " (" + strings.Join(parts, ", ") + ")"
		}
		required := "none"
		if len(spec.RequiredFields) > 0 {
			required = strings.Join(spec.RequiredFields, ", ")
		}
		forbidden := "none"
		if len(spec.ForbiddenFields) > 0 {
			forbidden = strings.Join(spec.ForbiddenFields, ", ")
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s |", spec.Name, required, optional, forbidden))
	}
	join := func(render func(*OperationSpec) string) string {
		parts := make([]string, 0, len(specs))
		for _, spec := range specs {
			parts = append(parts, render(spec))
		}
		return strings.Join(parts, "; ")
	}
	rows = append(rows, "")
	rows = append(rows, "Source kinds by operation: "+join(func(spec *OperationSpec) string {
		if len(spec.SourceKinds) == 0 {
			return spec.Name + "=none"
		}
		return spec.Name + "=" + strings.Join(spec.SourceKinds, ", ")
	})+".")
	rows = append(rows, "Source asset types by operation: "+join(func(spec *OperationSpec) string {
		if len(spec.SourceAssetTypes) == 0 {
			return spec.Name + "=none"
		}
		return spec.Name + "=" + strings.Join(spec.SourceAssetTypes, ", ")
	})+".")
	rows = append(rows, "Target kinds by operation: "+join(func(spec *OperationSpec) string {
		if len(spec.TargetKinds) == 0 {
			return spec.Name + "=none"
		}
		return spec.Name + "=" + strings.Join(spec.TargetKinds, ", ")
	})+".")
	rows = append(rows, "The add_sprite costumes array contains 1..12 entries. Each entry has name, source, and optional tags; the source must be a costume or image_generation source. add_sound accepts only a Scratch library sound source.")
	rows = append(rows, "")
	rows = append(rows, "RESOURCE_SOURCE_CONTRACT_JSON="+sourceSpecsJSON())
	rows = append(rows, "RESOURCE_OPERATION_CONTRACT_JSON="+OperationContractJSON(allowDestructiveResourceOps))
	return strings.Join(rows, "\n")
}

// BoundedActual limits a validation payload before it is echoed to the client.
func BoundedActual(value any, limit int) any {
	encoded, err := MarshalCompact(value)
	if err != nil {
		text := fmt.Sprintf("%v", value)
		return truncateRunes(text, limit)
	}
	if len([]rune(encoded)) <= limit {
		return value
	}
	return truncateRunes(encoded, limit)
}

func truncateRunes(value string, limit int) string {
	if limit < 3 {
		limit = 3
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-3]) + "..."
}

var _ = sort.Strings
