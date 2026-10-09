package spatial

import (
	"math"
	"testing"
)

func TestFromMap(t *testing.T) {
	p, err := FromMap(map[string]any{"x": int64(3), "y": 4.5})
	if err != nil || p.SRID != SRIDCartesian || p.X != 3 || p.Y != 4.5 {
		t.Fatalf("cartesian: %v %v", p, err)
	}
	p, err = FromMap(map[string]any{"longitude": 12.5, "latitude": 41.9, "height": 10.0})
	if err != nil || p.SRID != SRIDWGS84_3D || p.CRS() != "wgs-84-3d" {
		t.Fatalf("geographic 3d: %v %v", p, err)
	}
	p, err = FromMap(map[string]any{"x": 1.0, "y": 2.0, "crs": "wgs-84"})
	if err != nil || p.SRID != SRIDWGS84 {
		t.Fatalf("x/y with crs: %v %v", p, err)
	}
	for _, bad := range []map[string]any{
		{"x": 1.0},
		{"x": 1.0, "y": 2.0, "longitude": 1.0},
		{"longitude": 1.0, "latitude": 91.0},
		{"x": 1.0, "y": 2.0, "crs": "nope"},
		{"x": "a", "y": 2.0},
		{"x": 1.0, "y": 2.0, "z": 3.0, "crs": "cartesian"},
	} {
		if _, err := FromMap(bad); err == nil {
			t.Errorf("FromMap(%v) should fail", bad)
		}
	}
}

func TestDistance(t *testing.T) {
	a, _ := FromMap(map[string]any{"x": 0.0, "y": 0.0})
	b, _ := FromMap(map[string]any{"x": 3.0, "y": 4.0})
	if d, ok := Distance(a, b); !ok || d != 5 {
		t.Errorf("cartesian distance = %v %v", d, ok)
	}
	g1, _ := FromMap(map[string]any{"longitude": 0.0, "latitude": 0.0})
	g2, _ := FromMap(map[string]any{"longitude": 0.0, "latitude": 1.0})
	if d, ok := Distance(g1, g2); !ok || math.Abs(d-111319.54) > 0.01 {
		t.Errorf("one degree of latitude = %v %v", d, ok)
	}
	if _, ok := Distance(a, g1); ok {
		t.Error("different CRS must have no distance")
	}
}

func TestWithinBBox(t *testing.T) {
	ll, _ := FromMap(map[string]any{"longitude": 170.0, "latitude": -10.0})
	ur, _ := FromMap(map[string]any{"longitude": -170.0, "latitude": 10.0})
	in, _ := FromMap(map[string]any{"longitude": 179.0, "latitude": 0.0})
	out, _ := FromMap(map[string]any{"longitude": 0.0, "latitude": 0.0})
	if w, ok := WithinBBox(in, ll, ur); !ok || !w {
		t.Error("point across the antimeridian should be inside")
	}
	if w, _ := WithinBBox(out, ll, ur); w {
		t.Error("point at the prime meridian should be outside")
	}
}

func TestString(t *testing.T) {
	p, _ := FromMap(map[string]any{"x": 1.0, "y": 2.5})
	if s := p.String(); s != "point({srid: 7203, x: 1.0, y: 2.5})" {
		t.Errorf("String() = %s", s)
	}
}
