// Package vector implements the Cypher VECTOR value: a fixed-dimension array
// of integers or floats with an explicit coordinate type, plus the distance,
// similarity and norm functions defined on it. It depends only on the standard
// library.
package vector

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Type is the coordinate type of a vector.
type Type string

const (
	Integer64 Type = "INTEGER64"
	Integer32 Type = "INTEGER32"
	Integer16 Type = "INTEGER16"
	Integer8  Type = "INTEGER8"
	Float64   Type = "FLOAT64"
	Float32   Type = "FLOAT32"
)

// MaxDimension is the largest vector dimension Cypher allows.
const MaxDimension = 4096

// ParseType accepts a coordinate type name (any case; INTEGER and FLOAT are
// accepted as INTEGER64 and FLOAT64).
func ParseType(s string) (Type, bool) {
	switch strings.ToUpper(s) {
	case "INTEGER64", "INTEGER", "INT64", "INT":
		return Integer64, true
	case "INTEGER32", "INT32":
		return Integer32, true
	case "INTEGER16", "INT16":
		return Integer16, true
	case "INTEGER8", "INT8":
		return Integer8, true
	case "FLOAT64", "FLOAT", "DOUBLE":
		return Float64, true
	case "FLOAT32":
		return Float32, true
	}
	return "", false
}

// IsInteger reports whether the type holds integers.
func (t Type) IsInteger() bool {
	return t == Integer64 || t == Integer32 || t == Integer16 || t == Integer8
}

// Vector is a vector value. Exactly one of Ints and Floats is used, according
// to Type.
type Vector struct {
	Type   Type
	Ints   []int64
	Floats []float64
}

// Dimension is the number of coordinates.
func (v Vector) Dimension() int {
	if v.Type.IsInteger() {
		return len(v.Ints)
	}
	return len(v.Floats)
}

// Float returns coordinate i as a float.
func (v Vector) Float(i int) float64 {
	if v.Type.IsInteger() {
		return float64(v.Ints[i])
	}
	return v.Floats[i]
}

// Floats64 returns all coordinates as floats.
func (v Vector) Float64s() []float64 {
	out := make([]float64, v.Dimension())
	for i := range out {
		out[i] = v.Float(i)
	}
	return out
}

// New builds a vector from numbers (int64 or float64) of the given type.
// dimension must match len(values).
func New(values []any, dimension int, t Type) (Vector, error) {
	if dimension < 1 || dimension > MaxDimension {
		return Vector{}, fmt.Errorf("vector dimension %d is outside [1, %d]", dimension, MaxDimension)
	}
	if len(values) != dimension {
		return Vector{}, fmt.Errorf("vector has %d values but dimension %d was given", len(values), dimension)
	}
	v := Vector{Type: t}
	for i, x := range values {
		switch n := x.(type) {
		case int64:
			if t.IsInteger() {
				if !fits(n, t) {
					return Vector{}, fmt.Errorf("value %d at position %d does not fit %s", n, i, t)
				}
				v.Ints = append(v.Ints, n)
			} else {
				v.Floats = append(v.Floats, roundTo(float64(n), t))
			}
		case float64:
			if t.IsInteger() {
				return Vector{}, fmt.Errorf("value %v at position %d is not an integer, which %s requires", n, i, t)
			}
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return Vector{}, fmt.Errorf("vector coordinates must be finite")
			}
			f := roundTo(n, t)
			if math.IsInf(f, 0) {
				return Vector{}, fmt.Errorf("value %v at position %d does not fit %s", n, i, t)
			}
			v.Floats = append(v.Floats, f)
		default:
			return Vector{}, fmt.Errorf("vector coordinates must be numbers")
		}
	}
	return v, nil
}

func fits(n int64, t Type) bool {
	switch t {
	case Integer8:
		return n >= math.MinInt8 && n <= math.MaxInt8
	case Integer16:
		return n >= math.MinInt16 && n <= math.MaxInt16
	case Integer32:
		return n >= math.MinInt32 && n <= math.MaxInt32
	}
	return true
}

func roundTo(f float64, t Type) float64 {
	if t == Float32 {
		return float64(float32(f))
	}
	return f
}

// Parse reads the string form "[1, 2.5, 3]" used by vector('…', n, TYPE).
func Parse(s string) ([]any, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("a vector string must look like [1, 2, 3]")
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil, nil
	}
	var out []any
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		if i, err := strconv.ParseInt(part, 10, 64); err == nil {
			out = append(out, i)
		} else if f, err := strconv.ParseFloat(part, 64); err == nil {
			out = append(out, f)
		} else {
			return nil, fmt.Errorf("%q is not a number", part)
		}
	}
	return out, nil
}

// Equal reports whether two vectors have the same type and coordinates.
func Equal(a, b Vector) bool {
	if a.Type != b.Type || a.Dimension() != b.Dimension() {
		return false
	}
	for i := 0; i < a.Dimension(); i++ {
		if a.Float(i) != b.Float(i) {
			return false
		}
	}
	return true
}

// String renders the vector as vector([...], n, TYPE).
func (v Vector) String() string {
	parts := make([]string, v.Dimension())
	for i := range parts {
		if v.Type.IsInteger() {
			parts[i] = strconv.FormatInt(v.Ints[i], 10)
		} else {
			s := strconv.FormatFloat(v.Floats[i], 'g', -1, 64)
			if !strings.ContainsAny(s, ".eE") {
				s += ".0"
			}
			parts[i] = s
		}
	}
	return fmt.Sprintf("vector([%s], %d, %s)", strings.Join(parts, ", "), v.Dimension(), v.Type)
}

func same(a, b Vector) error {
	if a.Dimension() != b.Dimension() {
		return fmt.Errorf("vectors have different dimensions (%d and %d)", a.Dimension(), b.Dimension())
	}
	return nil
}

// Distance computes vector_distance for the metric EUCLIDEAN,
// EUCLIDEAN_SQUARED, MANHATTAN, COSINE (1 - cosine similarity), DOT (the
// negated dot product, so that smaller means closer) or HAMMING.
func Distance(a, b Vector, metric string) (float64, error) {
	if err := same(a, b); err != nil {
		return 0, err
	}
	n := a.Dimension()
	switch strings.ToUpper(metric) {
	case "EUCLIDEAN", "EUCLIDEAN_SQUARED":
		sum := 0.0
		for i := 0; i < n; i++ {
			d := a.Float(i) - b.Float(i)
			sum += d * d
		}
		if strings.EqualFold(metric, "EUCLIDEAN") {
			return math.Sqrt(sum), nil
		}
		return sum, nil
	case "MANHATTAN":
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += math.Abs(a.Float(i) - b.Float(i))
		}
		return sum, nil
	case "COSINE":
		c, err := cosine(a, b)
		return 1 - c, err
	case "DOT":
		return -dot(a, b), nil
	case "HAMMING":
		diff := 0.0
		for i := 0; i < n; i++ {
			if a.Float(i) != b.Float(i) {
				diff++
			}
		}
		return diff, nil
	}
	return 0, fmt.Errorf("unknown vector metric %q (expected EUCLIDEAN, EUCLIDEAN_SQUARED, MANHATTAN, COSINE, DOT or HAMMING)", metric)
}

func dot(a, b Vector) float64 {
	sum := 0.0
	for i := 0; i < a.Dimension(); i++ {
		sum += a.Float(i) * b.Float(i)
	}
	return sum
}

func cosine(a, b Vector) (float64, error) {
	na, nb := math.Sqrt(dot(a, a)), math.Sqrt(dot(b, b))
	if na == 0 || nb == 0 {
		return 0, fmt.Errorf("cosine similarity is undefined for a zero vector")
	}
	return dot(a, b) / (na * nb), nil
}

// Norm computes vector_norm for EUCLIDEAN or MANHATTAN.
func Norm(v Vector, kind string) (float64, error) {
	sum := 0.0
	switch strings.ToUpper(kind) {
	case "EUCLIDEAN":
		for i := 0; i < v.Dimension(); i++ {
			sum += v.Float(i) * v.Float(i)
		}
		return math.Sqrt(sum), nil
	case "MANHATTAN":
		for i := 0; i < v.Dimension(); i++ {
			sum += math.Abs(v.Float(i))
		}
		return sum, nil
	}
	return 0, fmt.Errorf("unknown vector norm %q (expected EUCLIDEAN or MANHATTAN)", kind)
}

// SimilarityCosine is vector.similarity.cosine: (1 + cos)/2, a score in [0, 1].
func SimilarityCosine(a, b Vector) (float64, error) {
	if err := same(a, b); err != nil {
		return 0, err
	}
	c, err := cosine(a, b)
	if err != nil {
		return 0, err
	}
	return (1 + c) / 2, nil
}

// SimilarityEuclidean is vector.similarity.euclidean: 1 / (1 + squared
// distance), a score in (0, 1].
func SimilarityEuclidean(a, b Vector) (float64, error) {
	d, err := Distance(a, b, "EUCLIDEAN_SQUARED")
	if err != nil {
		return 0, err
	}
	return 1 / (1 + d), nil
}
