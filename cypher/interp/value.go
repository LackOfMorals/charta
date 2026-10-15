// Package interp executes Cypher by interpreting the typed syntax tree in Go.
//
// Rows flow through the clauses of a query: MATCH expands each row by matching
// patterns against the graph, WITH/RETURN project and aggregate, UNWIND fans a
// row out over a list, and the updating clauses write through to SQLite. It
// gives exact openCypher semantics for any composition of clauses, at the cost of
// evaluating in Go; filters that SQLite can apply are pushed into the scans.
//
// The package depends only on cypher/syntax and database/sql: all graph access
// is plain parameterised SQL against the nodes and edges tables.
package interp

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/graingraph/graingraph/cypher/spatial"
	"github.com/graingraph/graingraph/cypher/temporal"
	"github.com/graingraph/graingraph/cypher/vector"
)

// Value types used at run time:
//
//	nil, bool, int64, float64, string, []any, map[string]any, *Node, *Rel, *Path

// Node is a graph node.
type Node struct {
	ID     int64
	Labels []string
	Props  map[string]any
	// Deleted is set once the node has been deleted in this statement.
	Deleted bool
}

// Rel is a relationship.
type Rel struct {
	ID         int64
	Type       string
	Start, End int64
	Props      map[string]any
	Deleted    bool
}

// Path is an alternating sequence of nodes and relationships: len(Nodes) ==
// len(Rels)+1.
type Path struct {
	Nodes []*Node
	Rels  []*Rel
}

func (n *Node) hasLabel(l string) bool {
	for _, x := range n.Labels {
		if x == l {
			return true
		}
	}
	return false
}

// typeName names the Cypher type of v for error messages.
func typeName(v any) string {
	switch x := v.(type) {
	case nil:
		return "Null"
	case bool:
		return "Boolean"
	case int64:
		return "Integer"
	case float64:
		return "Float"
	case string:
		return "String"
	case []any:
		return "List"
	case map[string]any:
		return "Map"
	case *Node:
		return "Node"
	case *Rel:
		return "Relationship"
	case *Path:
		return "Path"
	case temporal.Value:
		return temporalTypeName(x)
	case spatial.Point:
		return "Point"
	case vector.Vector:
		return "Vector"
	}
	return fmt.Sprintf("%T", v)
}

// normalize converts Go values of other numeric/list/map types into the
// canonical run-time representation.
func normalize(v any) any { return normalizeD(v, 0) }

func normalizeD(v any, d int) any {
	if d > maxValueDepth {
		panic(errValueTooDeep)
	}
	switch x := v.(type) {
	case nil, bool, int64, float64, string, *Node, *Rel, *Path, temporal.Value, spatial.Point, vector.Vector:
		return v
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeD(e, d+1)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	case []int64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	case []int:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = int64(e)
		}
		return out
	case []float64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	case []bool:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalizeD(e, d+1)
		}
		return out
	}
	return v
}

// ─── tri-valued equality ─────────────────────────────────────────────────────

// tri is a three-valued boolean: true, false or null (unknown).
type tri int8

const (
	triFalse tri = iota
	triTrue
	triNull
)

func triOf(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

func (t tri) value() any {
	switch t {
	case triTrue:
		return true
	case triFalse:
		return false
	}
	return nil
}

func isNumber(v any) bool {
	switch v.(type) {
	case int64, float64:
		return true
	}
	return false
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return math.NaN()
}

// equals implements Cypher's `=`: null with anything is null, values of
// different types are false.
func equals(a, b any) tri { return equalsD(a, b, 0) }

func equalsD(a, b any, d int) tri {
	if d > maxValueDepth {
		panic(errValueTooDeep)
	}
	if a == nil || b == nil {
		return triNull
	}
	switch x := a.(type) {
	case bool:
		y, ok := b.(bool)
		return triOf(ok && x == y)
	case string:
		y, ok := b.(string)
		return triOf(ok && x == y)
	case int64:
		switch y := b.(type) {
		case int64:
			return triOf(x == y)
		case float64:
			return triOf(float64(x) == y)
		}
		return triFalse
	case float64:
		switch y := b.(type) {
		case int64:
			return triOf(x == float64(y))
		case float64:
			return triOf(x == y)
		}
		return triFalse
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return triFalse
		}
		result := triTrue
		for i := range x {
			switch equalsD(x[i], y[i], d+1) {
			case triFalse:
				return triFalse
			case triNull:
				result = triNull
			}
		}
		return result
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return triFalse
		}
		result := triTrue
		for k, xv := range x {
			yv, present := y[k]
			if !present {
				return triFalse
			}
			switch equalsD(xv, yv, d+1) {
			case triFalse:
				return triFalse
			case triNull:
				result = triNull
			}
		}
		return result
	case *Node:
		y, ok := b.(*Node)
		return triOf(ok && x.ID == y.ID)
	case *Rel:
		y, ok := b.(*Rel)
		return triOf(ok && x.ID == y.ID)
	case temporal.Value:
		y, ok := b.(temporal.Value)
		return triOf(ok && temporal.Equal(x, y))
	case spatial.Point:
		y, ok := b.(spatial.Point)
		return triOf(ok && x == y)
	case vector.Vector:
		y, ok := b.(vector.Vector)
		return triOf(ok && vector.Equal(x, y))
	case *Path:
		y, ok := b.(*Path)
		if !ok || len(x.Nodes) != len(y.Nodes) || len(x.Rels) != len(y.Rels) {
			return triFalse
		}
		for i := range x.Nodes {
			if x.Nodes[i].ID != y.Nodes[i].ID {
				return triFalse
			}
		}
		for i := range x.Rels {
			if x.Rels[i].ID != y.Rels[i].ID {
				return triFalse
			}
		}
		return triTrue
	}
	return triFalse
}

// compare implements the ordering comparisons (<, <=, >, >=): it returns the
// sign of a-b and ok=false when the values are not comparable (the result is
// then null).
func compare(a, b any) (int, bool) { return compareD(a, b, 0) }

func compareD(a, b any, d int) (int, bool) {
	if d > maxValueDepth {
		panic(errValueTooDeep)
	}
	if a == nil || b == nil {
		return 0, false
	}
	switch x := a.(type) {
	case bool:
		y, ok := b.(bool)
		if !ok {
			return 0, false
		}
		switch {
		case x == y:
			return 0, true
		case !x:
			return -1, true
		}
		return 1, true
	case string:
		y, ok := b.(string)
		if !ok {
			return 0, false
		}
		return strings.Compare(x, y), true
	case int64:
		switch y := b.(type) {
		case int64:
			return cmpInt(x, y), true
		case float64:
			return cmpFloat(float64(x), y)
		}
		return 0, false
	case float64:
		switch y := b.(type) {
		case int64:
			return cmpFloat(x, float64(y))
		case float64:
			return cmpFloat(x, y)
		}
		return 0, false
	case []any:
		y, ok := b.([]any)
		if !ok {
			return 0, false
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			c, ok := compareD(x[i], y[i], d+1)
			if !ok {
				return 0, false
			}
			if c != 0 {
				return c, true
			}
		}
		return cmpInt(int64(len(x)), int64(len(y))), true
	case temporal.Value:
		y, ok := b.(temporal.Value)
		if !ok {
			return 0, false
		}
		return temporal.Compare(x, y)
	}
	return 0, false
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) (int, bool) {
	if math.IsNaN(a) || math.IsNaN(b) {
		return 0, false
	}
	switch {
	case a < b:
		return -1, true
	case a > b:
		return 1, true
	}
	return 0, true
}

// ─── orderability (ORDER BY, min, max) ───────────────────────────────────────

// orderRank gives the position of a value's type in the global sort order:
// ascending is MAP < NODE < RELATIONSHIP < LIST < PATH < STRING < BOOLEAN <
// NUMBER < NaN < NULL.
func orderRank(v any) int {
	switch x := v.(type) {
	case map[string]any:
		return 0
	case *Node:
		return 1
	case *Rel:
		return 2
	case []any:
		return 3
	case *Path:
		return 4
	case spatial.Point:
		return 5
	case vector.Vector:
		return 5
	case temporal.Value:
		// ZONED DATETIME < LOCAL DATETIME < DATE < ZONED TIME < LOCAL TIME < DURATION
		switch x.Kind() {
		case temporal.KindDateTime:
			return 6
		case temporal.KindLocalDateTime:
			return 7
		case temporal.KindDate:
			return 8
		case temporal.KindTime:
			return 9
		case temporal.KindLocalTime:
			return 10
		}
		return 11
	case string:
		return 12
	case bool:
		return 13
	case int64:
		return 14
	case float64:
		if math.IsNaN(x) {
			return 15
		}
		return 14
	case nil:
		return 16
	}
	return 17
}

// orderCompare is a total order over all values (null sorts last ascending).
func orderCompare(a, b any) int { return orderCompareD(a, b, 0) }

func orderCompareD(a, b any, d int) int {
	if d > maxValueDepth {
		panic(errValueTooDeep)
	}
	ra, rb := orderRank(a), orderRank(b)
	if ra != rb {
		return cmpInt(int64(ra), int64(rb))
	}
	switch x := a.(type) {
	case map[string]any:
		y := b.(map[string]any)
		kx, ky := sortedKeys(x), sortedKeys(y)
		for i := 0; i < len(kx) && i < len(ky); i++ {
			if c := strings.Compare(kx[i], ky[i]); c != 0 {
				return c
			}
			if c := orderCompareD(x[kx[i]], y[ky[i]], d+1); c != 0 {
				return c
			}
		}
		return cmpInt(int64(len(kx)), int64(len(ky)))
	case *Node:
		return cmpInt(x.ID, b.(*Node).ID)
	case *Rel:
		return cmpInt(x.ID, b.(*Rel).ID)
	case []any:
		y := b.([]any)
		for i := 0; i < len(x) && i < len(y); i++ {
			if c := orderCompareD(x[i], y[i], d+1); c != 0 {
				return c
			}
		}
		return cmpInt(int64(len(x)), int64(len(y)))
	case *Path:
		y := b.(*Path)
		for i := 0; i < len(x.Nodes) && i < len(y.Nodes); i++ {
			if c := cmpInt(x.Nodes[i].ID, y.Nodes[i].ID); c != 0 {
				return c
			}
			if i < len(x.Rels) && i < len(y.Rels) {
				if c := cmpInt(x.Rels[i].ID, y.Rels[i].ID); c != 0 {
					return c
				}
			}
		}
		return cmpInt(int64(len(x.Nodes)), int64(len(y.Nodes)))
	case spatial.Point:
		y, ok := b.(spatial.Point)
		if !ok { // a point and a vector share a rank; points sort first
			return -1
		}
		return spatial.Compare(x, y)
	case vector.Vector:
		y, ok := b.(vector.Vector)
		if !ok {
			return 1
		}
		return strings.Compare(x.String(), y.String())
	case temporal.Value:
		if c, ok := temporal.Compare(x, b.(temporal.Value)); ok {
			return c
		}
		return strings.Compare(x.String(), b.(temporal.Value).String())
	case string:
		return strings.Compare(x, b.(string))
	case bool:
		y := b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	case int64, float64:
		if math.IsNaN(toFloat(a)) {
			return 0
		}
		if xi, ok := a.(int64); ok {
			if yi, ok := b.(int64); ok {
				return cmpInt(xi, yi)
			}
		}
		c, _ := cmpFloat(toFloat(a), toFloat(b))
		return c
	}
	return 0
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ─── grouping keys ───────────────────────────────────────────────────────────

// groupKey returns a string that is equal for values that are equivalent for
// DISTINCT and grouping: numbers compare by value (1 and 1.0 are the same), null
// groups with null, and NaN with NaN.
func groupKey(v any) string {
	var sb strings.Builder
	writeKey(&sb, v)
	return sb.String()
}

func writeKey(sb *strings.Builder, v any) { writeKeyD(sb, v, 0) }

func writeKeyD(sb *strings.Builder, v any, d int) {
	if d > maxValueDepth {
		panic(errValueTooDeep)
	}
	switch x := v.(type) {
	case nil:
		sb.WriteString("n;")
	case bool:
		if x {
			sb.WriteString("bt;")
		} else {
			sb.WriteString("bf;")
		}
	case int64:
		sb.WriteString("d")
		sb.WriteString(strconv.FormatInt(x, 10))
		sb.WriteByte(';')
	case float64:
		switch {
		case math.IsNaN(x):
			sb.WriteString("dNaN;")
		case x == math.Trunc(x) && math.Abs(x) < 9.2e18:
			sb.WriteString("d")
			sb.WriteString(strconv.FormatInt(int64(x), 10))
			sb.WriteByte(';')
		default:
			sb.WriteString("d")
			sb.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
			sb.WriteByte(';')
		}
	case string:
		sb.WriteString("s")
		sb.WriteString(strconv.Itoa(len(x)))
		sb.WriteByte(':')
		sb.WriteString(x)
	case []any:
		sb.WriteString("l[")
		for _, e := range x {
			writeKeyD(sb, e, d+1)
		}
		sb.WriteString("];")
	case map[string]any:
		sb.WriteString("m{")
		for _, k := range sortedKeys(x) {
			sb.WriteString(strconv.Itoa(len(k)))
			sb.WriteByte(':')
			sb.WriteString(k)
			writeKeyD(sb, x[k], d+1)
		}
		sb.WriteString("};")
	case *Node:
		sb.WriteString("N" + strconv.FormatInt(x.ID, 10) + ";")
	case *Rel:
		sb.WriteString("R" + strconv.FormatInt(x.ID, 10) + ";")
	case temporal.Value:
		sb.WriteString("T" + strconv.Itoa(int(x.Kind())) + x.String() + ";")
	case spatial.Point:
		sb.WriteString("S" + x.String() + ";")
	case vector.Vector:
		sb.WriteString("V" + x.String() + ";")
	case *Path:
		sb.WriteString("P[")
		for i, n := range x.Nodes {
			sb.WriteString(strconv.FormatInt(n.ID, 10) + ",")
			if i < len(x.Rels) {
				sb.WriteString(strconv.FormatInt(x.Rels[i].ID, 10) + ",")
			}
		}
		sb.WriteString("];")
	default:
		fmt.Fprintf(sb, "?%v;", x)
	}
}

// maxValueDepth bounds how deeply nested a list or map the interpreter will
// walk. Statements can build arbitrarily deep values iteratively (reduce(), a
// chain of WITH clauses), and the walkers recurse once per level, so without a
// bound a crafted statement overflows the goroutine stack, which is fatal.
const maxValueDepth = 10000

// valueTooDeep is the panic raised past maxValueDepth; run converts it into an
// error for the statement.
type valueTooDeep struct{}

var errValueTooDeep = valueTooDeep{}

func (valueTooDeep) Error() string { return "value is nested too deeply" }

// nestingExceeds reports whether v holds lists or maps nested deeper than
// limit. It walks with an explicit stack, so the value cannot overflow it.
func nestingExceeds(v any, limit int) bool {
	type item struct {
		v any
		d int
	}
	stack := []item{{v, 0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch x := it.v.(type) {
		case []any:
			if it.d+1 > limit {
				return true
			}
			for _, e := range x {
				switch e.(type) {
				case []any, map[string]any:
					stack = append(stack, item{e, it.d + 1})
				}
			}
		case map[string]any:
			if it.d+1 > limit {
				return true
			}
			for _, e := range x {
				switch e.(type) {
				case []any, map[string]any:
					stack = append(stack, item{e, it.d + 1})
				}
			}
		}
	}
	return false
}
