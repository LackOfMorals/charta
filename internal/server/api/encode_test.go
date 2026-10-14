package api

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/LackOfMorals/charta"
	"github.com/LackOfMorals/charta/cypher/spatial"
	"github.com/LackOfMorals/charta/cypher/vector"
)

// sameJSON reports whether two values marshal to the same JSON.
func sameJSON(a, b any) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ja) == string(jb)
}

func TestEncodePlainScalars(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{nil, nil},
		{true, true},
		{int64(-42), int64(-42)},
		{int(7), int64(7)},
		{float64(1.5), 1.5},
		{float32(2.25), float64(2.25)},
		{"hi", "hi"},
		{[]string{"a", "b"}, []any{"a", "b"}},
		{[]int64{1, 2}, []any{int64(1), int64(2)}},
	}
	for _, c := range cases {
		got, err := encodePlain(c.in)
		if err != nil {
			t.Errorf("encodePlain(%v) error: %v", c.in, err)
			continue
		}
		if !sameJSON(got, c.want) {
			t.Errorf("encodePlain(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func gotString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		out := ""
		for _, e := range x {
			out += gotString(e)
		}
		return "[" + out + "]"
	case []string:
		out := ""
		for _, e := range x {
			out += e
		}
		return "[" + out + "]"
	case map[string]any:
		return mapString(x)
	}
	return ""
}

func mapString(m map[string]any) string {
	out := "{"
	for k, v := range m {
		out += k + "=" + gotString(v) + ","
	}
	return out + "}"
}

func TestEncodePlainPoint(t *testing.T) {
	got, err := encodePlain(spatial.Point{SRID: spatial.SRIDCartesian, X: 1.2, Y: 3.4})
	if err != nil {
		t.Fatal(err)
	}
	if got != "SRID=7203;POINT (1.2 3.4)" {
		t.Fatalf("got %q", got)
	}
	got, err = encodePlain(spatial.Point{SRID: spatial.SRIDCartesian3D, X: 1, Y: 2, Z: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got != "SRID=9157;POINT Z (1.0 2.0 3.0)" {
		t.Fatalf("got %q", got)
	}
}

func TestEncodeTypedScalars(t *testing.T) {
	check := func(in any, wantType, wantVal string) {
		t.Helper()
		got, err := encodeTyped(in, 2)
		if err != nil {
			t.Fatalf("encodeTyped(%v): %v", in, err)
		}
		tv, ok := got.(typedValue)
		if !ok {
			t.Fatalf("encodeTyped(%v) = %T", in, got)
		}
		if tv.Type != wantType {
			t.Errorf("encodeTyped(%v).$type = %q, want %q", in, tv.Type, wantType)
		}
		if gotString(tv.Value) != wantVal {
			t.Errorf("encodeTyped(%v)._value = %v, want %v", in, tv.Value, wantVal)
		}
	}
	check(nil, "Null", "")
	check(true, "Boolean", "")
	check(int64(123), "Integer", "123")
	check("hi", "String", "hi")
	check([]byte{0x01}, "Base64", "AQ==")
}

func TestFormatFloat(t *testing.T) {
	cases := map[float64]string{
		0:                    "0.0",
		4.56:                 "4.56",
		-0.2:                 "-0.2",
		123456:               "123456.0",
		3.2e9:                "3.2E9",
		2.0e-7:               "2.0E-7",
		1e7:                  "1.0E7",
		-1e-5:                "-1.0E-5",
		1e21:                 "1.0E21",
		1.5e-300:             "1.5E-300",
		5e-324:               "4.9E-324", // Java prints the closest two-digit decimal
		math.Copysign(0, -1): "-0.0",
		math.NaN():           "NaN",
		math.Inf(1):          "Infinity",
		math.Inf(-1):         "-Infinity",
	}
	for in, want := range cases {
		if got := formatFloat(in); got != want {
			t.Errorf("formatFloat(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestEncodeVectorTyped(t *testing.T) {
	// v1.1+: native Vector.
	got, err := encodeTyped(vector.Vector{Type: vector.Integer8, Ints: []int64{1, -5, 10}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	tv := got.(typedValue)
	if tv.Type != "Vector" {
		t.Fatalf("$type = %q", tv.Type)
	}
	vv := tv.Value.(map[string]any)
	if vv["coordinatesType"] != "INT8" {
		t.Fatalf("coordinatesType = %v", vv["coordinatesType"])
	}
	coords, _ := vv["coordinates"].([]string)
	if len(coords) != 3 || coords[0] != "1" || coords[1] != "-5" || coords[2] != "10" {
		t.Fatalf("coordinates = %v", coords)
	}

	// v1.0: Vector is not supported, falls back to Unsupported.
	got, err = encodeTyped(vector.Vector{Type: vector.Float32, Floats: []float64{1}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.(typedValue).Type != "Unsupported" {
		t.Fatalf("v1.0 should emit Unsupported, got %v", got)
	}
}

func TestEncodeTypedPath(t *testing.T) {
	p := charta.Path{
		Nodes: []charta.Node{
			{ElementId: "1", Labels: []string{"Person"}, Props: map[string]any{"name": "A"}},
			{ElementId: "2", Labels: []string{"Person"}, Props: map[string]any{}},
		},
		Relationships: []charta.Relationship{
			{ElementId: "3", Type: "KNOWS", StartElementId: "1", EndElementId: "2", Props: map[string]any{}},
		},
	}
	got, err := encodeTyped(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	tv := got.(typedValue)
	if tv.Type != "Path" {
		t.Fatalf("$type = %q", tv.Type)
	}
	items := tv.Value.([]any)
	if len(items) != 3 {
		t.Fatalf("path has %d items", len(items))
	}
	if items[0].(typedValue).Type != "Node" || items[1].(typedValue).Type != "Relationship" || items[2].(typedValue).Type != "Node" {
		t.Fatalf("path item types: %v", items)
	}
	rel := items[1].(typedValue).Value.(map[string]any)
	if rel["_type"] != "KNOWS" || rel["_start_node_element_id"] != "1" {
		t.Fatalf("rel = %v", rel)
	}
}
