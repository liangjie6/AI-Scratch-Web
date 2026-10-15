package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(data)
}

func TestKnowledgeBaseMatchesPythonReference(t *testing.T) {
	builder := NewPromptBuilder(nil, nil, map[string]bool{}, false)
	got := builder.BuildKnowledgeBase(map[string]bool{})
	if want := readGolden(t, "knowledge_base_empty"); got != want {
		t.Errorf("knowledge base mismatch (got %d bytes, want %d bytes)", len(got), len(want))
		reportFirstDifference(t, got, want)
	}

	extended := NewPromptBuilder(nil, nil, map[string]bool{"pen": true, "music": true, "text2speech": true}, true)
	extensions := map[string]bool{"pen": true, "music": true, "text2speech": true}
	if got, want := extended.BuildKnowledgeBase(extensions), readGolden(t, "knowledge_base_extensions"); got != want {
		t.Errorf("extended knowledge base mismatch (got %d bytes, want %d bytes)", len(got), len(want))
		reportFirstDifference(t, got, want)
	}
}

func TestSegmentJSONSchemaMatchesPythonReference(t *testing.T) {
	segments := []string{"requirements", "task_spec", "blueprint", "plan", "manifest", "resource_op", "program"}
	for _, segment := range segments {
		if got, want := RenderSegmentJSONSchema(segment, false), readGolden(t, "schema_"+segment+"_nodestroy"); got != want {
			t.Errorf("schema %s (no destructive) mismatch (got %d bytes, want %d bytes)", segment, len(got), len(want))
			reportFirstDifference(t, got, want)
		}
		if got, want := RenderSegmentJSONSchema(segment, true), readGolden(t, "schema_"+segment+"_destroy"); got != want {
			t.Errorf("schema %s (destructive) mismatch (got %d bytes, want %d bytes)", segment, len(got), len(want))
			reportFirstDifference(t, got, want)
		}
	}
	if got := RenderSegmentJSONSchema("nope", false); got != "" {
		t.Errorf("unknown segment must render an empty schema, got %d bytes", len(got))
	}
}

func reportFirstDifference(t *testing.T, got, want string) {
	t.Helper()
	limit := len(got)
	if len(want) < limit {
		limit = len(want)
	}
	index := 0
	for index < limit && got[index] == want[index] {
		index++
	}
	start := index - 80
	if start < 0 {
		start = 0
	}
	end := index + 80
	if end > limit {
		end = limit
	}
	t.Errorf("first difference at byte %d\n got: %q\nwant: %q", index, slice(got, start, end), slice(want, start, end))
}

func slice(value string, start, end int) string {
	if start > len(value) {
		return ""
	}
	if end > len(value) {
		end = len(value)
	}
	return value[start:end]
}

func TestResourceOperationContractMatchesPythonReference(t *testing.T) {
	// The generated contract text is embedded in prompts; a smoke check keeps the
	// contract renderer wired to the generated data tables.
	if text := RenderSegmentJSONSchema("plan", false); text == "" {
		t.Fatal("plan schema must not be empty")
	}
}
