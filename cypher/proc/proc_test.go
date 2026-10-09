package proc

import "testing"

func TestCoerce(t *testing.T) {
	tests := []struct {
		name string
		p    Param
		in   any
		want any
		ok   bool
	}{
		{"int to float", Param{Name: "x", Type: "FLOAT"}, int64(2), 2.0, true},
		{"float to int rejected", Param{Name: "x", Type: "INTEGER"}, 2.5, nil, false},
		{"number accepts both", Param{Name: "x", Type: "NUMBER"}, 1.5, 1.5, true},
		{"null needs nullable", Param{Name: "x", Type: "STRING"}, nil, nil, false},
		{"nullable null", Param{Name: "x", Type: "STRING", Nullable: true}, nil, nil, true},
		{"any", Param{Name: "x", Type: "ANY"}, "s", "s", true},
		{"wrong type", Param{Name: "x", Type: "BOOLEAN"}, "true", nil, false},
	}
	for _, tt := range tests {
		got, err := Coerce(tt.p, tt.in)
		if (err == nil) != tt.ok || (tt.ok && got != tt.want) {
			t.Errorf("%s: got %v, %v", tt.name, got, err)
		}
	}
}

func TestAccepts(t *testing.T) {
	if !(Param{Type: "FLOAT"}).Accepts("INTEGER") || (Param{Type: "INTEGER"}).Accepts("FLOAT") {
		t.Error("numeric acceptance")
	}
	if (Param{Type: "STRING"}).Accepts("NULL") || !(Param{Type: "STRING", Nullable: true}).Accepts("NULL") {
		t.Error("null acceptance")
	}
}

func TestSetLookupIsCaseInsensitive(t *testing.T) {
	var s Set
	s.Register(&Procedure{Signature: Signature{Name: "Test.Proc"}})
	if _, ok := s.Lookup("test.proc"); !ok {
		t.Error("lookup should ignore case")
	}
	s.Clear()
	if _, ok := s.Lookup("test.proc"); ok {
		t.Error("Clear should remove procedures")
	}
	var nilSet *Set
	if _, ok := nilSet.Get("x"); ok {
		t.Error("nil set must be empty")
	}
}
