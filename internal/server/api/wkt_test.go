package api

import (
	"testing"

	"github.com/graingraph/graingraph/cypher/spatial"
)

func TestParseWKT(t *testing.T) {
	cases := []struct {
		in   string
		want spatial.Point
	}{
		{"SRID=7203;POINT (1.2 3.4)", spatial.Point{SRID: 7203, X: 1.2, Y: 3.4}},
		{"SRID=9157;POINT Z (1.0 2.0 3.0)", spatial.Point{SRID: 9157, X: 1, Y: 2, Z: 3}},
		{"POINT (5 -5)", spatial.Point{SRID: 7203, X: 5, Y: -5}},
		{"SRID=4326;POINT (13.4 52.5)", spatial.Point{SRID: 4326, X: 13.4, Y: 52.5}},
	}
	for _, c := range cases {
		got, err := parseWKT(c.in)
		if err != nil {
			t.Errorf("parseWKT(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseWKT(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}

	bad := []string{
		"SRID=9999;POINT (1 2)",
		"POINT (1)",
		"POLYGON ((0 0, 1 1))",
		"SRID=7203;POINT (1 2 3)", // 3 coords without Z
	}
	for _, in := range bad {
		if _, err := parseWKT(in); err == nil {
			t.Errorf("parseWKT(%q) succeeded, want error", in)
		}
	}
}

func TestWKHTMLTRoundTrip(t *testing.T) {
	p := spatial.Point{SRID: spatial.SRIDWGS84, X: 13.404954, Y: 52.520008}
	s := wktOf(p)
	back, err := parseWKT(s)
	if err != nil {
		t.Fatalf("parseWKT(%q): %v", s, err)
	}
	if back != p {
		t.Fatalf("round trip = %+v, want %+v", back, p)
	}
}
