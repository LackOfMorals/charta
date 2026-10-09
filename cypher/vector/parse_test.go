package vector

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestParseNumberMatchesStrconv(t *testing.T) {
	cases := []string{"0", "-0", "1", "-1", "3.0", "0.5", "-0.5", "123456789", "0.000001", "1e3", "1E3", "1e+3", "1e-3",
		"1.5e10", "-2.5e-7", "12345678901234567890", "0.1234567890123456789", "1e22", "1e23", "1e-22", "1e-23", "9007199254740993",
		"3.4028235e38", "1.17549435e-38", "1e400", "5e-324", "100", "0.30000000000000004"}
	r := rand.New(rand.NewSource(5))
	for i := 0; i < 20000; i++ {
		cases = append(cases, strconv.FormatFloat(r.NormFloat64()*math.Pow(10, float64(r.Intn(30)-15)), 'g', -1, 64))
		cases = append(cases, strconv.FormatFloat(r.Float64()*2-1, 'g', -1, 64)) // 17 digits, like a stored embedding
		cases = append(cases, strconv.FormatFloat(float64(float32(r.NormFloat64())), 'g', -1, 32))
	}
	for _, s := range cases {
		want, err := strconv.ParseFloat(s, 64)
		got, next, ok := parseNumber(s, 0)
		if err != nil || math.IsInf(want, 0) {
			if ok {
				t.Errorf("%q: parsed %v but strconv rejects it", s, got)
			}
			continue
		}
		// Within 4e-16 relative error (a couple of ulps of a double): the
		// value only has to be right to float32 precision.
		if !ok || next != len(s) || math.Abs(got-want) > 4e-16*math.Abs(want) {
			t.Errorf("%q: got %v (%v, next %d), want %v", s, got, ok, next, want)
		}
	}
}

func TestParseFloat32s(t *testing.T) {
	dst := make([]float32, 3)
	if !ParseFloat32s(" [1, 2.5 ,-3e-1] ", dst) || dst[0] != 1 || dst[1] != 2.5 || dst[2] != float32(-0.3) {
		t.Errorf("good input: %v", dst)
	}
	for _, bad := range []string{"", "[]", "[1,2]", "[1,2,3,4]", "[1,2,x]", "[1,2,null]", "[1,2,3", "1,2,3", "[1 2 3]", `["a",1,2]`, "[1,,3]", "[1,2,NaN]", "[1,2,1e999]", "[1,2,1e39]"} {
		if ParseFloat32s(bad, dst) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestBuilderMatchesIncrementalBuild(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	const dim = 12
	for _, metric := range []Metric{Cosine, Euclidean} {
		b := NewBuilder(dim, metric)
		inc := NewMatrix(dim, metric)
		for id := int64(1); id <= 5000; id++ {
			v := randVec(r, dim)
			if id%97 == 0 {
				for i := range v {
					v[i] = 0 // zero vectors are dropped under cosine
				}
			}
			row := b.NextRow()
			copy(row, v)
			b.Keep(id)
			inc.Upsert(id, v)
			if id%13 == 0 { // a discarded row leaves no trace
				b.NextRow()
				b.Discard()
			}
		}
		built := b.Matrix()
		if built.Len() != inc.Len() {
			t.Fatalf("metric %d: %d rows, want %d", metric, built.Len(), inc.Len())
		}
		q := randVec(r, dim)
		got, _ := built.Search(bg(), q, 20)
		want, _ := inc.Search(bg(), q, 20)
		for i := range got {
			if got[i].ID != want[i].ID || abs32(got[i].Score-want[i].Score) > 1e-5 {
				t.Fatalf("metric %d rank %d: %v vs %v", metric, i, got[i], want[i])
			}
		}
		built.Upsert(999999, q) // a built matrix accepts updates
		if hits, _ := built.Search(bg(), q, 1); hits[0].ID != 999999 {
			t.Errorf("an upserted row should be found: %v", hits)
		}
	}
}

func BenchmarkParseFloat32s384(b *testing.B) {
	r := rand.New(rand.NewSource(7))
	parts := make([]string, 384)
	for i := range parts {
		parts[i] = strconv.FormatFloat(r.Float64()*2-1, 'g', -1, 64) // 17 digits, as stored
	}
	text := "[" + strings.Join(parts, ",") + "]"
	dst := make([]float32, 384)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !ParseFloat32s(text, dst) {
			b.Fatal("parse failed")
		}
	}
	_ = fmt.Sprint(dst[0])
}

func TestStoredVector(t *testing.T) {
	dst := make([]float32, 3)
	ok := func(props string) bool { return StoredVector(props, "e", dst) }
	if !ok(`{"a":"x","e":[1,2.5,-3],"z":[9]}`) || dst[0] != 1 || dst[1] != 2.5 || dst[2] != -3 {
		t.Errorf("list: %v", dst)
	}
	if !ok(`{"e":{"$v":"FLOAT32","c":[4,5,6]},"id":1}`) || dst[2] != 6 {
		t.Errorf("VECTOR object: %v", dst)
	}
	// Other properties with awkward content (brackets and quotes inside strings,
	// nested lists and objects) must be skipped correctly.
	if !ok(`{"a":"]}[{\"e\":","b":[[1],[2,3]],"c":{"e":[7,7,7]},"e":[1,1,1]}`) || dst[0] != 1 {
		t.Errorf("skipping: %v", dst)
	}
	for _, bad := range []string{`{}`, `{"x":1}`, `{"e":"str"}`, `{"e":[1,2]}`, `{"e":[1,2,3,4]}`, `{"e":null}`, `{"e":{"c":"x"}}`, `{"e":{"$v":"FLOAT32"}}`, `{"e":[1,2,"a"]}`, `[1,2,3]`, ``} {
		if ok(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func BenchmarkStoredVector384(b *testing.B) {
	r := rand.New(rand.NewSource(8))
	parts := make([]string, 384)
	for i := range parts {
		parts[i] = strconv.FormatFloat(r.Float64()*2-1, 'g', -1, 64) // 17 digits, as stored
	}
	props := `{"id":42,"name":"a document title","tags":["x","y","z"],"e":[` + strings.Join(parts, ",") + `],"zz":1}`
	dst := make([]float32, 384)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !StoredVector(props, "e", dst) {
			b.Fatal("failed")
		}
	}
}
