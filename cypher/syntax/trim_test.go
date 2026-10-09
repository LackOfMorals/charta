package syntax

import "testing"

func TestTrimForms(t *testing.T) {
	tests := []struct{ in, want string }{
		{"trim(s)", "trim"},
		{"trim(BOTH 'x' FROM s)", "btrim"},
		{"trim(LEADING 'x' FROM s)", "ltrim"},
		{"trim(TRAILING FROM s)", "rtrim"},
		{"trim('x' FROM s)", "btrim"},
		{"trim(both)", "trim"}, // a variable named both
	}
	for _, tt := range tests {
		e, err := ParseExpr(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		f, ok := e.(*FuncCall)
		if !ok || f.Name != tt.want {
			t.Errorf("%s parsed as %#v, want function %s", tt.in, e, tt.want)
		}
	}
	if _, err := ParseExpr("trim(LEADING 'x', s)"); err == nil {
		t.Error("trim(LEADING 'x', s) must be a syntax error")
	}
	e, _ := ParseExpr("normalize(s, NFKD)")
	if f := e.(*FuncCall); f.Args[1].(*StringLit).Value != "NFKD" {
		t.Errorf("normal form not converted: %#v", f.Args[1])
	}
}
