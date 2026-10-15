package schema

import (
	"strings"

	"github.com/ai-scratch-web/backend/internal/resourcecontract"
)

// SchemaValidationError is the Go analogue of a pydantic ValidationError for a
// single contract violation. Path uses JSON-pointer notation (for example
// /schema_version or /shared_state/variables/0/ref_id).
type SchemaValidationError struct {
	Path    string
	Message string
}

func (e *SchemaValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// ContractIssue is one (path, message) pair of a ProgramContractError.
type ContractIssue struct {
	Path    string
	Message string
}

// ProgramContractError ports ProgramContractError.
type ProgramContractError struct {
	Issues []ContractIssue
}

// NewProgramContractError builds a ProgramContractError from issues.
func NewProgramContractError(issues ...ContractIssue) *ProgramContractError {
	return &ProgramContractError{Issues: issues}
}

func (e *ProgramContractError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Path+": "+issue.Message)
	}
	return strings.Join(parts, "; ")
}

// ResourceContractIssue ports the frozen dataclass ResourceContractIssue.
type ResourceContractIssue struct {
	Code        string
	Path        string
	Operation   string
	Actual      any
	Expected    string
	RepairScope string
	RepairID    *string
}

// NewResourceContractIssue builds an issue with the default repair scope.
func NewResourceContractIssue(code, path, operation string, actual any, expected string, repairID *string) ResourceContractIssue {
	return ResourceContractIssue{
		Code:        code,
		Path:        path,
		Operation:   operation,
		Actual:      actual,
		Expected:    expected,
		RepairScope: DefaultRepairScope,
		RepairID:    repairID,
	}
}

// NewResourceContractIssueWithScope builds an issue with an explicit scope.
func NewResourceContractIssueWithScope(code, path, operation string, actual any, expected, repairScope string, repairID *string) ResourceContractIssue {
	return ResourceContractIssue{
		Code:        code,
		Path:        path,
		Operation:   operation,
		Actual:      actual,
		Expected:    expected,
		RepairScope: repairScope,
		RepairID:    repairID,
	}
}

// AsDict ports ResourceContractIssue.as_dict(). It always contains exactly the
// Python keys: code, path, operation, actual, expected, repair_scope, repair_id.
func (i ResourceContractIssue) AsDict() map[string]any {
	repairScope := i.RepairScope
	if repairScope == "" {
		repairScope = DefaultRepairScope
	}
	var repairID any
	if i.RepairID != nil {
		repairID = *i.RepairID
	}
	return map[string]any{
		"code":         i.Code,
		"path":         i.Path,
		"operation":    i.Operation,
		"actual":       resourcecontract.BoundedActual(i.Actual, MaxBoundedActual),
		"expected":     i.Expected,
		"repair_scope": repairScope,
		"repair_id":    repairID,
	}
}

// ResourceContractError ports ResourceContractError.
type ResourceContractError struct {
	Issue ResourceContractIssue
}

func (e *ResourceContractError) Error() string {
	return e.Issue.Code + " at " + e.Issue.Path + ": " + e.Issue.Expected
}

// resourceIssue ports _resource_issue.
func resourceIssue(raw map[string]any, code, path string, actual any, expected string) *ResourceContractError {
	operation := "unknown"
	if value, ok := raw["op"]; ok {
		operation = pythonString(value)
	}
	var repairID *string
	if value, ok := raw["op_id"].(string); ok {
		text := value
		repairID = &text
	}
	return &ResourceContractError{Issue: NewResourceContractIssue(code, path, operation, actual, expected, repairID)}
}

func pythonString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return PythonRepr(value)
}
