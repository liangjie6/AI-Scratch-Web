package prompt

import "testing"

func TestRepairHintForJSONDecodeError(t *testing.T) {
	hint := RepairHint("program", "/output", "Expecting ',' delimiter: line 1 column 2097 (char 2096)")
	for _, want := range []string{"花括号 '}' 闭合不足或缺失逗号", "Pretty-printed JSON", "嵌套 control_if"} {
		if !contains(hint, want) {
			t.Fatalf("repair hint %q is missing %q", hint, want)
		}
	}
}

func TestRepairHintForResourceOps(t *testing.T) {
	hint := RepairHint("manifest", "/resource_ops[1]/op", "")
	if !contains(hint, "这是数组第 2 项。") {
		t.Fatalf("repair hint must include the array index, got %q", hint)
	}
	if !contains(hint, "资源操作必须严格匹配资源契约") {
		t.Fatalf("repair hint must describe the resource contract, got %q", hint)
	}
}

func TestRepairHintPhaseFallback(t *testing.T) {
	if got := RepairHint("program", "", ""); !contains(got, "ProgramIR schema") {
		t.Fatalf("program fallback = %q", got)
	}
	if got := RepairHint("manifest", "", ""); !contains(got, "ProjectManifest v3") {
		t.Fatalf("manifest fallback = %q", got)
	}
	if got := RepairHint("", "", ""); !contains(got, "FINAL OUTPUT CONTRACT") {
		t.Fatalf("default fallback = %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
