package vector

import (
	"math"
	"testing"
)

func mk(t *testing.T, ty Type, vals ...any) Vector {
	t.Helper()
	v, err := New(vals, len(vals), ty)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNew(t *testing.T) {
	if _, err := New([]any{int64(1), int64(200)}, 2, Integer8); err == nil {
		t.Error("200 does not fit INTEGER8")
	}
	if _, err := New([]any{1.5}, 1, Integer32); err == nil {
		t.Error("a float is not an integer coordinate")
	}
	if _, err := New([]any{int64(1)}, 2, Float32); err == nil {
		t.Error("dimension mismatch")
	}
	v := mk(t, Float32, 0.1, int64(2))
	if v.Floats[0] != float64(float32(0.1)) {
		t.Errorf("FLOAT32 coordinates are rounded to float32, got %v", v.Floats[0])
	}
	if s := v.String(); s != "vector([0.10000000149011612, 2.0], 2, FLOAT32)" {
		t.Errorf("String() = %s", s)
	}
}

func TestDistances(t *testing.T) {
	a := mk(t, Float64, 1.0, 2.0, 3.0)
	b := mk(t, Float64, 4.0, 6.0, 3.0)
	cases := map[string]float64{"EUCLIDEAN": 5, "EUCLIDEAN_SQUARED": 25, "MANHATTAN": 7, "HAMMING": 2, "DOT": -(4 + 12 + 9)}
	for m, want := range cases {
		got, err := Distance(a, b, m)
		if err != nil || got != want {
			t.Errorf("%s = %v %v, want %v", m, got, err, want)
		}
	}
	c, err := Distance(a, a, "COSINE")
	if err != nil || math.Abs(c) > 1e-12 {
		t.Errorf("cosine distance to itself = %v %v", c, err)
	}
	if _, err := Distance(a, mk(t, Float64, 1.0), "EUCLIDEAN"); err == nil {
		t.Error("different dimensions must fail")
	}
	if n, _ := Norm(a, "MANHATTAN"); n != 6 {
		t.Errorf("manhattan norm = %v", n)
	}
	if s, _ := SimilarityEuclidean(a, a); s != 1 {
		t.Errorf("euclidean similarity to itself = %v", s)
	}
	if s, _ := SimilarityCosine(a, a); math.Abs(s-1) > 1e-12 {
		t.Errorf("cosine similarity to itself = %v", s)
	}
}

func TestParse(t *testing.T) {
	v, err := Parse("[1, 2.5, -3]")
	if err != nil || len(v) != 3 || v[0] != int64(1) || v[1] != 2.5 {
		t.Errorf("Parse = %v %v", v, err)
	}
	if _, err := Parse("1,2"); err == nil {
		t.Error("missing brackets")
	}
}
