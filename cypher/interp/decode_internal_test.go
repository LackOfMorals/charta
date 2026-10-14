package interp

import (
	"context"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/LackOfMorals/graingraph/cypher/spatial"
	"github.com/LackOfMorals/graingraph/cypher/temporal"
	"github.com/LackOfMorals/graingraph/store"
)

func newDBInternal(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var decodeSamples = []string{
	`{"category":"Cat3","id":1003,"incorporated":1983,"name":"Company 1003 Ltd","score":3,"status":"Active"}`,
	`{"id":17,"name":"Person 17"}`,
	`{"a":1.5,"b":true,"c":"x","d":[1,2,3],"e":{"$t":0,"v":"2020-01-02"}}`,
}

func BenchmarkDecodeProps(b *testing.B) {
	for i, s := range decodeSamples {
		b.Run([]string{"company6", "person2", "mixed"}[i], func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := decodeProps(s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInternNode(b *testing.B) {
	g := newGraph(nil, nil)
	props := decodeSamples[0]
	b.ReportAllocs()
	for i := range b.N {
		g.nodes = map[int64]*Node{} // a fresh cache, so every call decodes
		if _, err := g.internNode(int64(i), "Company", props); err != nil {
			b.Fatal(err)
		}
	}
}

// referenceDecodeProps is decodeProps as it was: every value, scalar or not,
// goes through reviveTemporal and is written back.
func referenceDecodeProps(s string) (map[string]any, error) {
	props := map[string]any{}
	if s == "" || s == "{}" {
		return props, nil
	}
	m, ok := fastDecodeObject(s)
	if !ok {
		var err error
		if m, err = decodePropsSlow(s); err != nil {
			return nil, err
		}
	}
	for k, v := range m {
		m[k] = reviveTemporal(v)
	}
	return m, nil
}

// decodeProps must give what it always gave, for every kind of stored value.
func TestDecodePropsMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for i := 0; i < 5000; i++ {
		props := map[string]any{}
		for k := r.Intn(12); k > 0; k-- {
			props[randString(r)] = randValue(r, 0)
		}
		enc, err := encodeProps(props)
		if err != nil {
			t.Fatal(err)
		}
		got, gerr := decodeProps(enc)
		want, werr := referenceDecodeProps(enc)
		if (gerr == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s\n got  %#v (%v)\n want %#v (%v)", enc, got, gerr, want, werr)
		}
	}
	for _, s := range []string{"", "{}", " {} ", `{"a":`, `not json`, `{"a":1}x`, `{"$t":0,"v":"2020-01-02"}`} {
		got, gerr := decodeProps(s)
		want, werr := referenceDecodeProps(s)
		if (gerr == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %#v (%v), want %#v (%v)", s, got, gerr, want, werr)
		}
	}
}

// Nodes with the same label list share one slice. Adding a label to one must
// not show on the others, even when two nodes gain labels in the same statement.
func TestSharedLabelSlicesAreNotAliased(t *testing.T) {
	db := newDBInternal(t)
	runInternal(t, db, "UNWIND range(1, 4) AS i CREATE (:A {i: i})", nil)
	runInternal(t, db, "MATCH (a:A {i: 1}), (b:A {i: 2}), (c:A {i: 3}) SET a:X, b:Y, c:X:Z", nil)
	got := runInternal(t, db, "MATCH (n:A) RETURN n.i AS i, labels(n) AS l ORDER BY i", nil)
	want := []string{"1|[A X]", "2|[A Y]", "3|[A X Z]", "4|[A]"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	runInternal(t, db, "MATCH (n:A {i: 1}) REMOVE n:A", nil)
	got = runInternal(t, db, "MATCH (n) RETURN n.i AS i, labels(n) AS l ORDER BY i", nil)
	want = []string{"1|[X]", "2|[A Y]", "3|[A X Z]", "4|[A]"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("after REMOVE: %v, want %v", got, want)
	}

	// The cached split slices have no spare capacity, so append cannot write into a shared array.
	g := newGraph(nil, nil)
	a, b := g.labelsOf("A,B"), g.labelsOf("A,B")
	if &a[0] != &b[0] || cap(a) != len(a) {
		t.Fatalf("label lists not shared with exact capacity: cap %d len %d", cap(a), len(a))
	}
}

// A temporal, point or vector inside a list is stored in its tagged form and must
// come back as the value, not the map: the revive pass has to reach into lists.
func TestDecodePropsRevivesValuesInsideLists(t *testing.T) {
	d, err := temporal.Parse(temporal.KindDate, "2020-02-29")
	if err != nil {
		t.Fatal(err)
	}
	dt, err := temporal.Parse(temporal.KindLocalDateTime, "2020-02-29T10:11:12")
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]any{
		"dates":  []any{d, d},
		"stamps": []any{dt},
		"points": []any{spatial.Point{SRID: 7203, X: 1, Y: 2}, spatial.Point{SRID: 7203, X: 3, Y: 4}},
		"one":    d,
		"plain":  []any{int64(1), int64(2)},
	}
	enc, err := encodeProps(props)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeProps(enc)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := referenceDecodeProps(enc)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %#v, reference %#v", got, want)
	}
	if _, ok := got["dates"].([]any)[0].(temporal.Value); !ok {
		t.Errorf("a date inside a list came back as %T", got["dates"].([]any)[0])
	}
	if _, ok := got["points"].([]any)[1].(spatial.Point); !ok {
		t.Errorf("a point inside a list came back as %T", got["points"].([]any)[1])
	}
}

// In a statement that only reads, a scanned node is not remembered; elsewhere it
// is, because a writing statement compares and tracks nodes by pointer.
func TestScanPageCachesOnlyForWritingStatements(t *testing.T) {
	db := newDBInternal(t)
	runInternal(t, db, "UNWIND range(1, 40) AS i CREATE (:A {i: i})", nil)
	for _, evictable := range []bool{true, false} {
		g := newGraph(context.Background(), db.DB())
		g.evictable = evictable
		nodes, _, raw, err := g.scanPage("A", nil, 0, 100)
		if err != nil || len(nodes) != 40 || raw != 40 {
			t.Fatalf("scanPage: %d nodes, %d rows, %v", len(nodes), raw, err)
		}
		if cached := len(g.nodes); evictable && cached != 0 || !evictable && cached != 40 {
			t.Errorf("evictable=%v: %d nodes cached", evictable, cached)
		}
		// A node already cached comes back as that very node.
		g2 := newGraph(context.Background(), db.DB())
		g2.evictable = true
		first, _ := g2.node(nodes[0].ID)
		again, _, _, _ := g2.scanPage("A", nil, 0, 100)
		if again[0] != first {
			t.Errorf("a cached node was replaced by a second copy")
		}
	}
}
