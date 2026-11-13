package blockcatalog

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

type generatedPort struct {
	Kind          InputKind `json:"kind"`
	Required      bool      `json:"required"`
	Resolver      string    `json:"resolver"`
	ResourceType  AssetType `json:"resourceType"`
	AllowedValues []string  `json:"allowedValues"`
	MenuProvider  string    `json:"menuProvider"`
	BuiltinValues []string  `json:"builtinValues"`
}

func TestGeneratedContractsMatchCapabilities(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/scratch-capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	var capabilities struct {
		CatalogHash string `json:"catalog_hash"`
		Sources     struct {
			Packages map[string]string `json:"packages"`
		} `json:"sources"`
		Blocks map[string]struct {
			InputOrder []string `json:"input_order"`
			Authoring  struct {
				Shape     Shape                    `json:"shape"`
				Targets   []string                 `json:"targets"`
				Fields    map[string]generatedPort `json:"fields"`
				Inputs    map[string]generatedPort `json:"inputs"`
				Substacks []string                 `json:"substacks"`
			} `json:"authoring"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(data, &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.CatalogHash != CatalogHash {
		t.Fatal("capability and Go hashes differ")
	}
	if !reflect.DeepEqual(capabilities.Sources.Packages, SourceVersions) {
		t.Fatal("source package versions differ")
	}
	if len(capabilities.Blocks) != len(BlockContracts) {
		t.Fatal("opcode allowlists differ")
	}
	for opcode, expected := range capabilities.Blocks {
		t.Run(opcode, func(t *testing.T) {
			actual, found := BlockContracts[opcode]
			if !found {
				t.Fatal("opcode absent from Go catalog")
			}
			if actual.Shape != expected.Authoring.Shape || !slices.Equal(actual.InputOrder, expected.InputOrder) {
				t.Fatal("shape/input order differs")
			}
			if len(actual.Targets) != len(expected.Authoring.Targets) || len(actual.Fields) != len(expected.Authoring.Fields) || len(actual.Inputs) != len(expected.Authoring.Inputs) || len(actual.Substacks) != len(expected.Authoring.Substacks) {
				t.Fatal("port or target set differs")
			}
			for _, target := range expected.Authoring.Targets {
				if !actual.Targets[target] {
					t.Fatal("missing target", target)
				}
			}
			for _, substack := range expected.Authoring.Substacks {
				if !actual.Substacks[substack] || !actual.RequiredSubstacks[substack] {
					t.Fatal("missing substack", substack)
				}
			}
			for name, port := range expected.Authoring.Fields {
				if !actual.Fields[name] || actual.RequiredFields[name] != port.Required || actual.ResolvedFields[name] != (port.Resolver != "") || actual.ResourceFieldTypes[name] != port.ResourceType || !slices.Equal(actual.FieldEnums[name], port.AllowedValues) || actual.FieldProviders[name] != port.MenuProvider || !slices.Equal(actual.FieldBuiltins[name], port.BuiltinValues) {
					t.Fatal("field contract differs", name)
				}
			}
			for name, port := range expected.Authoring.Inputs {
				if actual.Inputs[name] != port.Kind || actual.RequiredInputs[name] != port.Required || actual.ResourceInputTypes[name] != port.ResourceType || !slices.Equal(actual.InputEnums[name], port.AllowedValues) || actual.InputProviders[name] != port.MenuProvider || !slices.Equal(actual.InputBuiltins[name], port.BuiltinValues) {
					t.Fatal("input contract differs", name)
				}
			}
		})
	}
}

func TestSourceMetadataDoesNotExpandAuthoringPolicy(t *testing.T) {
	for opcode, port := range map[string]string{"data_setvariableto": "VALUE", "data_addtolist": "ITEM", "data_insertatlist": "ITEM", "data_replaceitemoflist": "ITEM"} {
		if BlockContracts[opcode].Inputs[port] != InputAny {
			t.Fatalf("%s.%s must preserve native numeric and text state values", opcode, port)
		}
	}
	if BlockContracts["looks_say"].Inputs["MESSAGE"] != InputText {
		t.Fatal("state value policy widened an unrelated text port")
	}
	if BlockContracts["control_stop"].Shape != ShapeCap {
		t.Fatal("stop continuation variant was enabled")
	}
	if _, found := BlockContracts["control_while"]; found {
		t.Fatal("hidden native opcode became model-callable")
	}
	if !slices.Contains(BlockContracts["event_whenkeypressed"].FieldEnums["KEY_OPTION"], "i") {
		t.Fatal("fixture variable removed a valid key")
	}
	if BlockContracts["motion_goto"].InputProviders["TO"] != "sprites_except_self" {
		t.Fatal("dynamic menu was frozen into a static enum")
	}
}
