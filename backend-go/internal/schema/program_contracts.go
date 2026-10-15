package schema

import (
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/ai-scratch-web/backend/internal/blockcatalog"
	"github.com/ai-scratch-web/backend/internal/jsonx"
)

// ValidateProgramForMode ports validate_program_for_mode.
func ValidateProgramForMode(program *ProgramIR, mode string, currentProgram ...string) (*ProgramIR, error) {
	context := ""
	if len(currentProgram) > 0 {
		context = currentProgram[0]
	}
	if (mode == "append" || mode == "rewrite") && len(program.RemoveScriptIDs) > 0 {
		return nil, errors.New(mode + " mode cannot remove scripts by id")
	}
	if mode == "modify" && len(program.RemoveScriptIDs) > 0 && context != "" {
		parsed, err := jsonx.Parse([]byte(context))
		if err != nil {
			return nil, err
		}
		object, ok := parsed.(map[string]any)
		if !ok {
			return nil, errors.New("current_program must contain a JSON object")
		}
		scripts, ok := object["scripts"].([]any)
		if !ok {
			if _, present := object["scripts"]; present {
				return nil, errors.New("current_program scripts must be a list")
			}
			scripts = []any{}
		}
		protected := map[string]bool{}
		for _, item := range scripts {
			script, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if script["protected"] == true {
				if id, ok := script["id"].(string); ok {
					protected[id] = true
				}
			}
		}
		for _, id := range program.RemoveScriptIDs {
			if protected[id] {
				return nil, errors.New("modify mode cannot remove protected scripts")
			}
		}
	}
	return program, nil
}

// ValidateProjectProgramSharedState ports validate_project_program_shared_state.
func ValidateProjectProgramSharedState(program *ProgramIR, sharedState *ProjectSharedState, targetKind ...string) (*ProgramIR, error) {
	resolvedTargetKind := "sprite"
	if len(targetKind) > 0 && targetKind[0] != "" {
		resolvedTargetKind = targetKind[0]
	}
	if program.SchemaVersion == 2 {
		if len(program.Variables) > 0 || len(program.Lists) > 0 || len(program.Broadcasts) > 0 {
			return nil, NewProgramContractError(ContractIssue{
				Path:    "$.shared_state",
				Message: "ProgramIR v2 project programs must not declare variables, lists, or broadcasts",
			})
		}
		return program, nil
	}
	issues := []ContractIssue{}
	effectiveScope := func(scope *string) string {
		if resolvedTargetKind == "stage" {
			return "global"
		}
		if scope == nil {
			return "local"
		}
		return *scope
	}
	dumpState := func(name string, value any, isList bool, scope string) map[string]any {
		if isList && value == nil {
			value = []any{}
		}
		return map[string]any{"name": name, "value": value, "scope": scope}
	}
	sortByName := func(values []map[string]any) {
		sort.SliceStable(values, func(i, j int) bool {
			left, _ := values[i]["name"].(string)
			right, _ := values[j]["name"].(string)
			return left < right
		})
	}
	expectedVariables := make([]map[string]any, 0, len(sharedState.Variables))
	for index := range sharedState.Variables {
		item := &sharedState.Variables[index]
		expectedVariables = append(expectedVariables, dumpState(item.Name, item.Value, false, "global"))
	}
	expectedLists := make([]map[string]any, 0, len(sharedState.Lists))
	for index := range sharedState.Lists {
		item := &sharedState.Lists[index]
		expectedLists = append(expectedLists, dumpState(item.Name, item.Value, true, "global"))
	}
	actualVariables := make([]map[string]any, 0, len(program.Variables))
	for index := range program.Variables {
		item := &program.Variables[index]
		if effectiveScope(item.Scope) != "global" {
			continue
		}
		actualVariables = append(actualVariables, dumpState(item.Name, item.Value, false, "global"))
	}
	actualLists := make([]map[string]any, 0, len(program.Lists))
	for index := range program.Lists {
		item := &program.Lists[index]
		if effectiveScope(item.Scope) != "global" {
			continue
		}
		actualLists = append(actualLists, dumpState(item.Name, item.Value, true, "global"))
	}
	sortByName(expectedVariables)
	sortByName(expectedLists)
	sortByName(actualVariables)
	sortByName(actualLists)
	if !reflect.DeepEqual(actualVariables, expectedVariables) {
		issues = append(issues, ContractIssue{"$.variables", "project program global variables must exactly repeat manifest.shared_state.variables"})
	}
	if !reflect.DeepEqual(actualLists, expectedLists) {
		issues = append(issues, ContractIssue{"$.lists", "project program global lists must exactly repeat manifest.shared_state.lists"})
	}
	programBroadcasts := append([]string{}, program.Broadcasts...)
	sort.Strings(programBroadcasts)
	expectedBroadcasts := make([]string, 0, len(sharedState.Broadcasts))
	for index := range sharedState.Broadcasts {
		expectedBroadcasts = append(expectedBroadcasts, sharedState.Broadcasts[index].Name)
	}
	sort.Strings(expectedBroadcasts)
	if !reflect.DeepEqual(programBroadcasts, expectedBroadcasts) {
		issues = append(issues, ContractIssue{"$.broadcasts", "project program broadcasts must exactly repeat manifest.shared_state.broadcasts"})
	}
	globalNames := map[string]bool{}
	for _, item := range expectedVariables {
		if name, ok := item["name"].(string); ok {
			globalNames[name] = true
		}
	}
	for _, item := range expectedLists {
		if name, ok := item["name"].(string); ok {
			globalNames[name] = true
		}
	}
	for _, group := range []struct {
		path   string
		values []VariableDefinition
	}{
		{"$.variables", program.Variables},
	} {
		for index := range group.values {
			item := &group.values[index]
			if effectiveScope(item.Scope) != "global" && globalNames[item.Name] {
				issues = append(issues, ContractIssue{group.path, "target-local state cannot reuse a manifest global name"})
			}
		}
	}
	for index := range program.Lists {
		item := &program.Lists[index]
		if effectiveScope(item.Scope) != "global" && globalNames[item.Name] {
			issues = append(issues, ContractIssue{"$.lists", "target-local state cannot reuse a manifest global name"})
		}
	}
	if len(issues) > 0 {
		return nil, &ProgramContractError{Issues: issues}
	}
	return program, nil
}

// ValidateProjectProgramResourceRefs ports validate_project_program_resource_refs.
func ValidateProjectProgramResourceRefs(program *ProgramIR, manifest *ProjectManifest, existingAssets map[string][]string) (*ProgramIR, error) {
	resourceTypes := map[string]string{}
	for _, operation := range manifest.ResourceOps {
		opID, _ := operation["op_id"].(string)
		switch resourceOpName(operation) {
		case "add_sprite", "add_builtin_sprite":
			resourceTypes[opID] = "sprite"
		case "add_costume":
			resourceTypes[opID] = "costume"
		case "add_backdrop":
			resourceTypes[opID] = "backdrop"
		case "add_sound":
			resourceTypes[opID] = "sound"
		}
	}
	issues := []ContractIssue{}
	validateResourceRef := func(value any, required string, requiredKnown bool, path string) {
		if ref, ok := AsResourceRefInputValue(value); ok {
			opID := ""
			if ref.Ref != nil {
				opID = ref.Ref.OpID
			} else if ref.OpID != nil {
				opID = *ref.OpID
			}
			actual, known := resourceTypes[opID]
			if !known {
				issues = append(issues, ContractIssue{path + ".ref.op_id", "resource_ref must reference a manifest resource operation"})
			} else if ref.ResourceType != nil && actual != *ref.ResourceType {
				issues = append(issues, ContractIssue{path + ".resource_type", "resource_ref type does not match the manifest resource"})
			}
			if !requiredKnown {
				issues = append(issues, ContractIssue{path, "resource_ref is only valid for a typed Scratch resource menu"})
			} else if actual != required {
				issues = append(issues, ContractIssue{path + ".ref", "resource_ref type is not valid for this Scratch menu"})
			}
		} else if existing, ok := AsExistingAssetRefInputValue(value); ok {
			switch {
			case !requiredKnown:
				issues = append(issues, ContractIssue{path, "existing_asset_ref is only valid for a typed Scratch resource menu"})
			case existing.AssetType != required:
				issues = append(issues, ContractIssue{path + ".asset_type", "existing_asset_ref type is not valid for this Scratch menu"})
			case existingAssets != nil:
				names := existingAssets[required]
				if !containsString(names, existing.Name) {
					issues = append(issues, ContractIssue{path + ".name", "existing_asset_ref must name an existing asset in the target snapshot"})
				} else if countString(names, existing.Name) != 1 {
					issues = append(issues, ContractIssue{path + ".name", "existing_asset_ref must resolve to exactly one asset in the target snapshot"})
				}
			}
		}
	}
	var visit func(blocks []Block, path string)
	visit = func(blocks []Block, path string) {
		for index := range blocks {
			block := &blocks[index]
			blockPath := path + "[" + itoa(index) + "]"
			contract, hasContract := blockcatalog.BlockContracts[block.Opcode]
			for _, fieldName := range block.FieldNames() {
				value := block.Fields[fieldName]
				if !isResourceRefUnionValue(value) {
					continue
				}
				required := ""
				requiredKnown := false
				if hasContract {
					if assetType, ok := contract.ResourceFieldTypes[fieldName]; ok {
						required = string(assetType)
						requiredKnown = true
					}
				}
				validateResourceRef(value, required, requiredKnown, blockPath+".fields."+fieldName)
			}
			for _, inputName := range block.InputNames() {
				value := block.Inputs[inputName]
				required := ""
				requiredKnown := false
				if hasContract {
					if assetType, ok := contract.ResourceInputTypes[inputName]; ok {
						required = string(assetType)
						requiredKnown = true
					}
				}
				switch {
				case isResourceRefUnionValue(value):
					validateResourceRef(value, required, requiredKnown, blockPath+".inputs."+inputName)
				case isExistingAssetRefUnionValue(value):
					validateResourceRef(value, required, requiredKnown, blockPath+".inputs."+inputName)
				default:
					if blockInput, ok := AsBlockInputValue(value); ok {
						visit([]Block{blockInput.Block}, blockPath+".inputs."+inputName+".block")
					}
				}
			}
			for _, stackName := range block.SubstacksNames() {
				visit(block.Substacks[stackName], blockPath+".substacks."+stackName)
			}
		}
	}
	for scriptIndex := range program.Scripts {
		visit(program.Scripts[scriptIndex].Blocks, "$.scripts["+itoa(scriptIndex)+"].blocks")
	}
	for procedureIndex := range program.Procedures {
		visit(program.Procedures[procedureIndex].Body, "$.procedures["+itoa(procedureIndex)+"].body")
	}
	if len(issues) > 0 {
		return nil, &ProgramContractError{Issues: issues}
	}
	return program, nil
}

func countString(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func isResourceRefUnionValue(value any) bool {
	object, ok := value.(map[string]any)
	return ok && object["kind"] == "resource_ref"
}

func isExistingAssetRefUnionValue(value any) bool {
	object, ok := value.(map[string]any)
	return ok && object["kind"] == "existing_asset_ref"
}

// ValidateProjectProgramStateRefs ports validate_project_program_state_refs.
func ValidateProjectProgramStateRefs(program *ProgramIR, manifest *ProjectManifest) (*ProgramIR, error) {
	known := map[string]string{}
	for index := range manifest.SharedState.Variables {
		known[manifest.SharedState.Variables[index].RefID] = "variable"
	}
	for index := range manifest.SharedState.Lists {
		known[manifest.SharedState.Lists[index].RefID] = "list"
	}
	for index := range manifest.SharedState.Broadcasts {
		known[manifest.SharedState.Broadcasts[index].RefID] = "broadcast"
	}
	for index := range manifest.LocalState {
		known[manifest.LocalState[index].RefID] = manifest.LocalState[index].Kind
	}
	issues := []ContractIssue{}
	var visit func(blocks []Block, path string)
	visit = func(blocks []Block, path string) {
		for index := range blocks {
			block := &blocks[index]
			blockPath := path + "[" + itoa(index) + "]"
			for _, fieldName := range block.FieldNames() {
				value := block.Fields[fieldName]
				stateRef, ok := AsStateRefFieldValue(value)
				if !ok {
					continue
				}
				actual, present := known[stateRef.RefID]
				if !present {
					issues = append(issues, ContractIssue{blockPath + ".fields." + fieldName + ".ref_id", "state_ref must reference manifest shared_state or local_state"})
				} else if actual != stateRef.StateKind {
					issues = append(issues, ContractIssue{blockPath + ".fields." + fieldName + ".state_kind", "state_ref type does not match manifest state"})
				}
			}
			for _, inputName := range block.InputNames() {
				if blockInput, ok := AsBlockInputValue(block.Inputs[inputName]); ok {
					visit([]Block{blockInput.Block}, blockPath+".inputs."+inputName+".block")
				}
			}
			for _, stackName := range block.SubstacksNames() {
				visit(block.Substacks[stackName], blockPath+".substacks."+stackName)
			}
		}
	}
	for scriptIndex := range program.Scripts {
		visit(program.Scripts[scriptIndex].Blocks, "$.scripts["+itoa(scriptIndex)+"].blocks")
	}
	for procedureIndex := range program.Procedures {
		visit(program.Procedures[procedureIndex].Body, "$.procedures["+itoa(procedureIndex)+"].body")
	}
	if len(issues) > 0 {
		return nil, &ProgramContractError{Issues: issues}
	}
	return program, nil
}

// ProgramContractOptions carries the optional arguments of
// validate_program_contracts.
type ProgramContractOptions struct {
	TargetKind        string
	AllowedExtensions map[string]bool
	AllowResourceRefs bool
}

// DefaultProgramContractOptions mirrors the Python defaults: sprite target, no
// allowed extensions and logical resource refs enabled.
func DefaultProgramContractOptions() ProgramContractOptions {
	return ProgramContractOptions{
		TargetKind:        "sprite",
		AllowedExtensions: map[string]bool{},
		AllowResourceRefs: true,
	}
}

// ValidateProgramContracts ports validate_program_contracts.
func ValidateProgramContracts(program *ProgramIR, options ...ProgramContractOptions) (*ProgramIR, error) {
	resolved := DefaultProgramContractOptions()
	if len(options) > 0 {
		resolved = options[0]
		if resolved.TargetKind == "" {
			resolved.TargetKind = "sprite"
		}
		if resolved.AllowedExtensions == nil {
			resolved.AllowedExtensions = map[string]bool{}
		}
	}
	targetKind := resolved.TargetKind
	allowedExtensions := resolved.AllowedExtensions
	allowResourceRefs := resolved.AllowResourceRefs && GetDeploymentSettings().AILogicalResourceRefsEnabled
	issues := []ContractIssue{}
	procedures := map[string]*Procedure{}
	for index := range program.Procedures {
		procedures[program.Procedures[index].ID] = &program.Procedures[index]
	}
	issue := func(path, message string) {
		issues = append(issues, ContractIssue{Path: path, Message: message})
	}
	type visitOptions struct {
		nestedReporter     bool
		expectedReporter   string
		procedureArguments map[string]*ProcedureParameter
	}
	var visitSequence func(blocks []Block, path string, script bool)
	var visit func(block *Block, path string, options visitOptions)
	visitSequence = func(blocks []Block, path string, script bool) {
		for index := range blocks {
			block := &blocks[index]
			blockPath := path + "[" + itoa(index) + "]"
			contract, ok := blockcatalog.BlockContracts[block.Opcode]
			if !ok {
				issue(blockPath+".opcode", "unknown opcode")
				continue
			}
			if script && index == 0 && contract.Shape != blockcatalog.ShapeHat {
				issue(blockPath, "script must start with an event hat block")
			}
			if contract.Shape == blockcatalog.ShapeHat && ((script && index > 0) || !script) {
				issue(blockPath, "event hat block cannot appear here")
			}
			if contract.Shape == blockcatalog.ShapeCap && index < len(blocks)-1 {
				issue(blockPath, "cap block must be the last block in its stack")
			}
			visit(block, blockPath, visitOptions{nestedReporter: false})
		}
	}
	visit = func(block *Block, path string, options visitOptions) {
		if block.Opcode == "procedures_call" {
			if options.nestedReporter {
				issue(path, "procedure calls cannot be nested reporters")
			}
			if block.ArgumentID != nil {
				issue(path+".argument_id", "procedure call cannot include argument_id")
			}
			procedure := procedures[stringValue(block.ProcedureID)]
			if procedure == nil {
				issue(path+".procedure_id", "must reference a declared procedure")
				return
			}
			expectedParameters := map[string]*ProcedureParameter{}
			for index := range procedure.Parameters {
				expectedParameters[procedure.Parameters[index].ID] = &procedure.Parameters[index]
			}
			for _, name := range block.InputNames() {
				value := block.Inputs[name]
				parameter := expectedParameters[name]
				if parameter == nil {
					issue(path+".inputs."+name, "does not match a procedure parameter")
					continue
				}
				if blockInput, ok := AsBlockInputValue(value); ok {
					expectedReporter := "text"
					if parameter.Kind == "boolean" {
						expectedReporter = "boolean"
					}
					visit(&blockInput.Block, path+".inputs."+name+".block", visitOptions{
						nestedReporter:     true,
						expectedReporter:   expectedReporter,
						procedureArguments: options.procedureArguments,
					})
				} else {
					kind, _ := inputDiscriminator(value)
					if parameter.Kind == "boolean" || kind != "text" {
						issue(path+".inputs."+name, "does not match the procedure parameter type")
					}
				}
			}
			for index := range procedure.Parameters {
				parameterID := procedure.Parameters[index].ID
				if _, present := block.Inputs[parameterID]; !present {
					issue(path+".inputs."+parameterID, "required procedure argument is missing")
				}
			}
			if len(block.Fields) > 0 {
				issue(path+".fields", "procedure call cannot include fields")
			}
			if len(block.Substacks) > 0 {
				issue(path+".substacks", "procedure call cannot include substacks")
			}
			return
		}
		if block.Opcode == "argument_reporter_string_number" || block.Opcode == "argument_reporter_boolean" {
			if !options.nestedReporter {
				issue(path, "procedure argument reporter must be nested in an input")
			}
			if block.ProcedureID != nil {
				issue(path+".procedure_id", "argument reporter cannot include procedure_id")
			}
			var parameter *ProcedureParameter
			if options.procedureArguments != nil {
				parameter = options.procedureArguments[stringValue(block.ArgumentID)]
			}
			if parameter == nil {
				issue(path+".argument_id", "must reference a parameter of the current procedure")
			} else {
				expectedKind := "string_number"
				if block.Opcode == "argument_reporter_boolean" {
					expectedKind = "boolean"
				}
				if parameter.Kind != expectedKind {
					issue(path+".opcode", "reporter kind does not match the procedure parameter")
				}
				if options.expectedReporter == "boolean" && parameter.Kind != "boolean" {
					issue(path, "input requires a boolean reporter")
				}
			}
			if len(block.Fields) > 0 || len(block.Inputs) > 0 || len(block.Substacks) > 0 {
				issue(path, "procedure argument reporter cannot include fields, inputs, or substacks")
			}
			return
		}
		contract, ok := blockcatalog.BlockContracts[block.Opcode]
		if !ok {
			issue(path+".opcode", "unknown opcode")
			return
		}
		if !contract.Targets[targetKind] {
			issue(path+".opcode", "not valid for "+targetKind)
		}
		extension := blockcatalog.ExtensionForOpcode(block.Opcode)
		if extension != "" && !allowedExtensions[extension] {
			issue(path+".opcode", "extension "+extension+" is not declared for this program")
		}
		if block.ProcedureID != nil {
			issue(path+".procedure_id", "is only valid for procedures_call")
		}
		if block.ArgumentID != nil {
			issue(path+".argument_id", "is only valid for a procedure argument reporter")
		}
		if options.nestedReporter {
			if contract.Shape != blockcatalog.ShapeReporter && contract.Shape != blockcatalog.ShapeBoolean {
				issue(path, "nested input block must be a reporter or boolean")
			} else if options.expectedReporter == "boolean" && contract.Shape != blockcatalog.ShapeBoolean {
				issue(path, "input requires a boolean block")
			}
		} else if contract.Shape == blockcatalog.ShapeReporter || contract.Shape == blockcatalog.ShapeBoolean {
			issue(path, "reporter blocks must be nested in an input")
		}
		for _, name := range block.FieldNames() {
			if !contract.Fields[name] {
				issue(path+".fields."+name, "unknown field")
			}
		}
		for _, name := range sortedStringSet(contract.RequiredFields) {
			if _, present := block.Fields[name]; !present {
				issue(path+".fields."+name, "required field is missing")
			}
		}
		for _, name := range sortedStringSet(contract.ResolvedFields) {
			if value, present := block.Fields[name]; present {
				if _, isString := value.(string); !isString {
					if _, isStateRef := AsStateRefFieldValue(value); !isStateRef {
						issue(path+".fields."+name, "resolved field must use a name string or state_ref")
					}
				}
			}
			if stateRef, ok := AsStateRefFieldValue(block.Fields[name]); ok {
				expectedKind := "variable"
				if name == "LIST" {
					expectedKind = "list"
				} else if name == "BROADCAST_OPTION" {
					expectedKind = "broadcast"
				}
				if stateRef.StateKind != expectedKind {
					issue(path+".fields."+name+".state_kind", "resolved field requires "+expectedKind+" state_ref")
				}
			}
		}
		for _, name := range block.FieldNames() {
			value := block.Fields[name]
			resourceType, hasResourceType := contract.ResourceFieldTypes[name]
			if ref, ok := AsResourceRefInputValue(value); ok {
				switch {
				case !allowResourceRefs:
					issue(path+".fields."+name+".kind", "resource_ref is only valid for project programs with a Manifest binding table")
				case !hasResourceType:
					issue(path+".fields."+name+".kind", "resource_ref is only valid for a typed Scratch resource field menu")
				case ref.ResourceType != nil && *ref.ResourceType != string(resourceType):
					issue(path+".fields."+name+".resource_type", "resource_ref must be "+string(resourceType)+" for this Scratch field menu")
				}
			} else if existing, ok := AsExistingAssetRefInputValue(value); ok {
				if !hasResourceType {
					issue(path+".fields."+name+".kind", "existing_asset_ref is only valid for a typed Scratch resource field menu")
				} else if existing.AssetType != string(resourceType) {
					issue(path+".fields."+name+".asset_type", "existing_asset_ref must be "+string(resourceType)+" for this Scratch field menu")
				}
			}
		}
		for _, name := range block.InputNames() {
			value := block.Inputs[name]
			expected, hasExpected := contract.Inputs[name]
			if !hasExpected {
				issue(path+".inputs."+name, "unknown input")
				continue
			}
			inputPath := path + ".inputs." + name
			if blockInput, ok := AsBlockInputValue(value); ok {
				visit(&blockInput.Block, inputPath+".block", visitOptions{
					nestedReporter:     true,
					expectedReporter:   string(expected),
					procedureArguments: options.procedureArguments,
				})
			} else if ref, ok := AsResourceRefInputValue(value); ok {
				resourceType, hasResourceType := contract.ResourceInputTypes[name]
				switch {
				case !allowResourceRefs:
					issue(inputPath+".kind", "resource_ref is only valid for project programs with a Manifest binding table")
				case expected != "text" || !hasResourceType:
					issue(inputPath+".kind", "resource_ref is only valid for a typed Scratch resource menu")
				case ref.ResourceType != nil && *ref.ResourceType != string(resourceType):
					issue(inputPath+".resource_type", "resource_ref must be "+string(resourceType)+" for this Scratch menu")
				}
			} else if existing, ok := AsExistingAssetRefInputValue(value); ok {
				resourceType, hasResourceType := contract.ResourceInputTypes[name]
				if expected != "text" || !hasResourceType {
					issue(inputPath+".kind", "existing_asset_ref is only valid for a typed Scratch resource menu")
				} else if existing.AssetType != string(resourceType) {
					issue(inputPath+".asset_type", "existing_asset_ref must be "+string(resourceType)+" for this Scratch menu")
				}
			} else if expected == "boolean" {
				issue(inputPath, "boolean input requires a nested boolean block")
			} else if expected != "any" {
				kind, _ := inputDiscriminator(value)
				if kind != string(expected) {
					issue(inputPath+".kind", "expected "+string(expected))
				}
			}
		}
		for _, name := range sortedStringSet(contract.RequiredInputs) {
			if _, present := block.Inputs[name]; !present {
				issue(path+".inputs."+name, "required input is missing")
			}
		}
		for _, name := range block.SubstacksNames() {
			if !contract.Substacks[name] {
				issue(path+".substacks."+name, "substack not allowed for opcode")
			}
		}
		for _, name := range sortedStringSet(contract.RequiredSubstacks) {
			if _, present := block.Substacks[name]; !present {
				issue(path+".substacks."+name, "required substack is missing")
			}
		}
		for _, name := range block.SubstacksNames() {
			visitSequence(block.Substacks[name], path+".substacks."+name, false)
		}
	}
	for scriptIndex := range program.Scripts {
		visitSequence(program.Scripts[scriptIndex].Blocks, "$.scripts["+itoa(scriptIndex)+"].blocks", true)
	}
	for procedureIndex := range program.Procedures {
		procedure := &program.Procedures[procedureIndex]
		arguments := map[string]*ProcedureParameter{}
		for index := range procedure.Parameters {
			arguments[procedure.Parameters[index].ID] = &procedure.Parameters[index]
		}
		for blockIndex := range procedure.Body {
			block := &procedure.Body[blockIndex]
			path := "$.procedures[" + itoa(procedureIndex) + "].body[" + itoa(blockIndex) + "]"
			if strings.HasPrefix(block.Opcode, "event_") || block.Opcode == "procedures_call" {
				issue(path+".opcode", "procedure body cannot contain event hats or procedure calls")
			}
			visit(block, path, visitOptions{nestedReporter: false, procedureArguments: arguments})
		}
	}
	if len(issues) > 0 {
		return nil, &ProgramContractError{Issues: issues}
	}
	return program, nil
}
