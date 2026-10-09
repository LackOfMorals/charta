package vector

import (
	"math"
	"math/rand"
	"testing"
)

func randVec(r *rand.Rand, n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return v
}

func refDot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func refL2(a, b []float32) float64 {
	var s float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		s += d * d
	}
	return s
}

func nearly(got float32, want float64) bool {
	return math.Abs(float64(got)-want) <= 1e-4*math.Max(1, math.Abs(want))
}

func TestKernelsMatchReference(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, dim := range []int{1, 2, 3, 7, 8, 9, 15, 16, 17, 100, 383, 384, 385, 768, 4096} {
		a, b := randVec(r, dim), randVec(r, dim)
		if got, want := Dot32(a, b), refDot(a, b); !nearly(got, want) {
			t.Errorf("dim %d: Dot32 = %v, want %v", dim, got, want)
		}
		if got, want := SquaredL2(a, b), refL2(a, b); !nearly(got, want) {
			t.Errorf("dim %d: SquaredL2 = %v, want %v", dim, got, want)
		}
		if got, want := Norm32(a), math.Sqrt(refDot(a, a)); !nearly(got, want) {
			t.Errorf("dim %d: Norm32 = %v, want %v", dim, got, want)
		}
		cos, ok := Cosine32(a, b)
		wantCos := refDot(a, b) / (math.Sqrt(refDot(a, a)) * math.Sqrt(refDot(b, b)))
		if !ok || !nearly(cos, wantCos) {
			t.Errorf("dim %d: Cosine32 = %v (%v), want %v", dim, cos, ok, wantCos)
		}
	}
}

func TestNormalize32(t *testing.T) {
	v := []float32{3, 4}
	if !Normalize32(v) || math.Abs(float64(Norm32(v))-1) > 1e-6 {
		t.Errorf("Normalize32 gave %v", v)
	}
	zero := []float32{0, 0, 0}
	if Normalize32(zero) {
		t.Error("a zero vector cannot be normalised")
	}
	if _, ok := Cosine32(zero, v); ok {
		t.Error("cosine with a zero vector is undefined")
	}
}

func naiveDot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

var sink float32

func benchKernel(b *testing.B, f func(a, b []float32) float32) {
	r := rand.New(rand.NewSource(2))
	const dim, n = 384, 20000
	flat := randVec(r, dim*n)
	q := randVec(r, dim)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var best float32 = -math.MaxFloat32
		for k := 0; k < n; k++ {
			if s := f(q, flat[k*dim:(k+1)*dim]); s > best {
				best = s
			}
		}
		sink = best
	}
}

func BenchmarkNaiveDot(b *testing.B)  { benchKernel(b, naiveDot) }
func BenchmarkDot32(b *testing.B)     { benchKernel(b, Dot32) }
func BenchmarkSquaredL2(b *testing.B) { benchKernel(b, SquaredL2) }
