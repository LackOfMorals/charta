package api

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func nestedTyped(depth int, leaf string) string {
	s := `{"$type":"String","_value":"` + leaf + `"}`
	for i := 0; i < depth; i++ {
		s = `{"$type":"List","_value":[` + s + `]}`
	}
	return s
}

func TestTypedParametersNestingBounded(t *testing.T) {
	leaf := strings.Repeat("A", 1<<20)
	raw := json.RawMessage(`{"p":` + nestedTyped(5000, leaf) + `}`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := decodeParameters(raw, formatTyped, 2); err == nil {
		t.Fatal("expected a nesting error")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 512<<20 {
		t.Fatalf("allocated %d MiB for a 1 MiB body", grew>>20)
	}
	ok := json.RawMessage(`{"p":` + nestedTyped(10, "x") + `}`)
	if _, err := decodeParameters(ok, formatTyped, 2); err != nil {
		t.Fatalf("shallow nesting must work: %v", err)
	}
}

func TestNonFiniteTypedValuesRejected(t *testing.T) {
	for _, body := range []string{
		`{"p":{"$type":"Point","_value":"POINT (NaN 1)"}}`,
		`{"p":{"$type":"Vector","_value":{"coordinatesType":"FLOAT64","coordinates":["NaN"]}}}`,
		`{"p":{"$type":"Vector","_value":{"coordinatesType":"FLOAT64","coordinates":["Inf"]}}}`,
	} {
		if _, err := decodeParameters(json.RawMessage(body), formatTyped, 2); err == nil {
			t.Errorf("%s: expected an error", body)
		}
	}
}
