package interp

import (
	"encoding/json"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/graingraph/graingraph/cypher/spatial"
	"github.com/graingraph/graingraph/cypher/temporal"
	"github.com/graingraph/graingraph/cypher/vector"
)

// referenceEncodeProps is the straightforward encoder encodeProps replaced:
// every key and string goes through json.Marshal.
func referenceEncodeProps(props map[string]any) (string, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteByte(':')
		if err := referenceEncodeValue(&sb, props[k]); err != nil {
			return "", err
		}
	}
	sb.WriteByte('}')
	return sb.String(), nil
}

func referenceEncodeValue(sb *strings.Builder, v any) error {
	if x, ok := v.(string); ok {
		b, _ := json.Marshal(x)
		sb.Write(b)
		return nil
	}
	if l, ok := v.([]any); ok {
		sb.WriteByte('[')
		for i, e := range l {
			if i > 0 {
				sb.WriteByte(',')
			}
			if err := referenceEncodeValue(sb, e); err != nil {
				return err
			}
		}
		sb.WriteByte(']')
		return nil
	}
	return encodeValue(sb, v) // every other kind is unchanged
}

var awkwardStrings = []string{
	"", "plain", "Company 1 Ltd", "with space", "a\"quote", `back\slash`, "tab\there", "new\nline", "\x00nul", "\x1f",
	"<html>&amp;</html>", "café", "日本語", "emoji 😀", "line sep", "para sep", "bad\xffutf8", "\xc3\x28",
	"del\x7f", "~tilde", "{\"json\":true}", "'single'", "slash/forward", "\u0080", "ÿ",
}

func randString(r *rand.Rand) string {
	if r.Intn(3) == 0 {
		return awkwardStrings[r.Intn(len(awkwardStrings))]
	}
	n := r.Intn(12)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Intn(256)) // any byte, so invalid UTF-8 appears too
	}
	return string(b)
}

func randValue(r *rand.Rand, depth int) any {
	switch r.Intn(9) {
	case 0:
		return r.Int63() - r.Int63()
	case 1:
		return r.NormFloat64() * 1e6
	case 2:
		return float64(r.Intn(100)) // integral float: written with ".0"
	case 3:
		return r.Intn(2) == 0
	case 4:
		return randString(r)
	case 5:
		if depth < 2 {
			n := r.Intn(4)
			l := make([]any, n)
			s := randString(r)
			for i := range l {
				l[i] = s + string(rune('a'+i))
			}
			return l
		}
		return randString(r)
	case 6:
		return spatial.Point{SRID: 7203, X: r.Float64(), Y: r.Float64()}
	case 7:
		return vector.Vector{Type: vector.Float32, Floats: []float64{r.Float64(), r.Float64()}}
	}
	v, err := temporal.Parse(temporal.KindDate, "2020-02-29")
	if err != nil {
		panic(err)
	}
	return v
}

// encodeProps must agree with the reference encoder byte for byte: the text is
// what is stored, compared and decoded.
func TestEncodePropsMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 5000; i++ {
		props := map[string]any{}
		for k := r.Intn(25); k > 0; k-- {
			props[randString(r)] = randValue(r, 0)
		}
		got, gerr := encodeProps(props)
		want, werr := referenceEncodeProps(props)
		if (gerr == nil) != (werr == nil) || got != want {
			t.Fatalf("props %#v\n got  %q (%v)\n want %q (%v)", props, got, gerr, want, werr)
		}
		if gerr == nil && !json.Valid([]byte(got)) && utf8.ValidString(got) {
			t.Fatalf("encodeProps produced invalid JSON: %q", got)
		}
	}
}

func TestWriteJSONStringMatchesMarshal(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	check := func(s string) {
		t.Helper()
		var sb strings.Builder
		writeJSONString(&sb, s)
		want, _ := json.Marshal(s)
		if sb.String() != string(want) {
			t.Fatalf("%q: got %s want %s", s, sb.String(), want)
		}
	}
	for _, s := range awkwardStrings {
		check(s)
	}
	for b := 0; b < 256; b++ { // every single byte, alone and inside text
		check(string([]byte{byte(b)}))
		check("ab" + string([]byte{byte(b)}) + "cd")
	}
	for i := 0; i < 20000; i++ {
		check(randString(r))
	}
}
