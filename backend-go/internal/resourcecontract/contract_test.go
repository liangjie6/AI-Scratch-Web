package resourcecontract

import (
	"os"
	"path/filepath"
	"testing"
)

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(data)
}

func TestRenderOperationContractMatchesPythonReference(t *testing.T) {
	if got, want := RenderOperationContract(true), golden(t, "contract_destroy"); got != want {
		t.Errorf("destructive contract mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
	if got, want := RenderOperationContract(false), golden(t, "contract_nodestroy"); got != want {
		t.Errorf("non-destructive contract mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
}

func TestOperationContractJSONMatchesPythonReference(t *testing.T) {
	if got, want := OperationContractJSON(true), golden(t, "contract_json_destroy"); got != want {
		t.Errorf("destructive contract JSON mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
	if got, want := OperationContractJSON(false), golden(t, "contract_json_nodestroy"); got != want {
		t.Errorf("non-destructive contract JSON mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
}

func TestBoundedActualMatchesPythonReference(t *testing.T) {
	short := map[string]any{"a": 1}
	if got := BoundedActual(short, 2048); got == nil {
		t.Fatal("short payload must be returned unchanged")
	}
	long := ""
	for index := 0; index < 3000; index++ {
		long += "x"
	}
	truncated, ok := BoundedActual(long, 2048).(string)
	if !ok {
		t.Fatal("long payload must be truncated to a string")
	}
	if len([]rune(truncated)) != 2048 {
		t.Fatalf("truncated payload must be 2048 runes, got %d", len([]rune(truncated)))
	}
	if truncated[len(truncated)-3:] != "..." {
		t.Fatal("truncated payload must end with an ellipsis")
	}
}
