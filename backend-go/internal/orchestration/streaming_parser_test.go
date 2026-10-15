package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRobustJSONParseValid(t *testing.T) {
	sample := `{"schema_version": 2, "summary": "test", "scripts": []}`
	parsed, err := RobustJSONParse(sample, 5)
	if err != nil {
		t.Fatalf("valid JSON must parse: %v", err)
	}
	if value, _ := parsed["schema_version"].(int64); value != 2 {
		t.Fatalf("schema_version = %v", parsed["schema_version"])
	}
	if parsed["summary"] != "test" {
		t.Fatalf("summary = %v", parsed["summary"])
	}
}

func TestRobustJSONParseRecoversMissingSubstacksBraces(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "malformed_program.json"))
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	sample := string(data)
	var probe map[string]any
	if err := json.Unmarshal([]byte(sample), &probe); err == nil {
		t.Fatal("the sample is supposed to be invalid JSON")
	}
	repaired, err := RobustJSONParse(sample, 5)
	if err != nil {
		t.Fatalf("robust parse must repair the sample: %v", err)
	}
	if value, _ := repaired["schema_version"].(int64); value != 2 {
		t.Fatalf("schema_version = %v", repaired["schema_version"])
	}
	scripts, ok := repaired["scripts"].([]any)
	if !ok || len(scripts) != 4 {
		t.Fatalf("scripts = %#v", repaired["scripts"])
	}
	ids := make([]string, 0, 4)
	for _, item := range scripts {
		script, _ := item.(map[string]any)
		id, _ := script["id"].(string)
		ids = append(ids, id)
	}
	want := []string{"script_cat_init", "script_cat_left", "script_cat_right", "script_cat_catch"}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("script ids = %v, want %v", ids, want)
		}
	}
}

func TestRobustJSONParseRecoversUnclosedTail(t *testing.T) {
	truncated := `{"schema_version": 2, "summary": "incomplete", "scripts": [{"id": "s1"`
	repaired, err := RobustJSONParse(truncated, 5)
	if err != nil {
		t.Fatalf("robust parse must complete the truncated tail: %v", err)
	}
	if value, _ := repaired["schema_version"].(int64); value != 2 {
		t.Fatalf("schema_version = %v", repaired["schema_version"])
	}
	scripts, _ := repaired["scripts"].([]any)
	if len(scripts) != 1 {
		t.Fatalf("scripts = %#v", repaired["scripts"])
	}
	first, _ := scripts[0].(map[string]any)
	if first["id"] != "s1" {
		t.Fatalf("script id = %v", first["id"])
	}
}

func TestRobustJSONParseRejectsNonObject(t *testing.T) {
	if _, err := RobustJSONParse("[1,2,3]", 5); err == nil {
		t.Fatal("a JSON array frame must be rejected")
	}
}

func TestUnifiedFrameParserStreamsTaggedFrames(t *testing.T) {
	parser := NewUnifiedFrameParser()
	chunks := []string{
		"<require",
		"ments>{\"ready\":true}</requirements>\n<plan>{\"steps\":[\"a\"]}</plan>",
		"<program target_index=0>",
		"<metadata>{\"schema_version\":1}</metadata>",
		"<script index=1 total=1>{\"id\":\"s1\"}</script>",
		"</program>",
	}
	collected := []StreamFrame{}
	for _, chunk := range chunks {
		frames, err := parser.Feed(chunk)
		if err != nil {
			t.Fatalf("feed(%q): %v", chunk, err)
		}
		collected = append(collected, frames...)
	}
	tail, err := parser.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	collected = append(collected, tail...)

	kinds := make([]string, 0, len(collected))
	for _, frame := range collected {
		kinds = append(kinds, frame.Kind)
	}
	want := "requirements,plan,program_start,metadata,script,program_end"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("frame kinds = %v, want %s", kinds, want)
	}
	if collected[2].Attrs["target_index"] != "0" {
		t.Fatalf("program attrs = %v", collected[2].Attrs)
	}
	if collected[4].Attrs["target_index"] != "0" || collected[4].Attrs["index"] != "1" {
		t.Fatalf("script attrs = %v", collected[4].Attrs)
	}
}

func TestUnifiedFrameParserRejectsUnexpectedOutput(t *testing.T) {
	parser := NewUnifiedFrameParser()
	if _, err := parser.Feed("hello"); err == nil {
		t.Fatal("unexpected top-level output must be rejected")
	}
}

func TestUnifiedFrameParserRejectsIncompleteProgram(t *testing.T) {
	parser := NewUnifiedFrameParser()
	if _, err := parser.Feed("<program target_index=0><metadata>{}</metadata>"); err != nil {
		t.Fatalf("feed: %v", err)
	}
	if _, err := parser.Finish(); err == nil {
		t.Fatal("an unterminated program frame must be rejected")
	}
}

func TestParseAttrsRejectsDuplicates(t *testing.T) {
	parser := NewUnifiedFrameParser()
	if _, err := parser.Feed("<script index=1 index=2 total=1>{}"); err == nil {
		t.Fatal("duplicate attributes must be rejected")
	}
}

// TestUnifiedFrameParserAcceptsQuotedAttributes locks the fix for a parser
// panic on double-quoted tag attributes, which every realistic project stream
// uses (for example target_index="0").
func TestUnifiedFrameParserAcceptsQuotedAttributes(t *testing.T) {
	parser := NewUnifiedFrameParser()
	frames, err := parser.Feed("<program target_index=\"0\"><script index='1' total=\"1\">{}</script></program>")
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	kinds := make([]string, 0, len(frames))
	for _, frame := range frames {
		kinds = append(kinds, frame.Kind)
	}
	if strings.Join(kinds, ",") != "program_start,script,program_end" {
		t.Fatalf("frame kinds = %v", kinds)
	}
	if frames[0].Attrs["target_index"] != "0" {
		t.Fatalf("program attrs = %v", frames[0].Attrs)
	}
	if frames[1].Attrs["index"] != "1" || frames[1].Attrs["total"] != "1" || frames[1].Attrs["target_index"] != "0" {
		t.Fatalf("script attrs = %v", frames[1].Attrs)
	}
}
