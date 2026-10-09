package interp

import (
	"math"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/temporal"
)

// A value type as written after `IS ::` or reported by valueType(), reduced to
// a canonical form.
type vtype struct {
	name    string  // INTEGER, STRING, LIST, ANY, …
	bits    int     // integer/float width for INT8…INT64, FLOAT32/FLOAT64; 0 = default
	elem    []vtype // element type alternatives of a LIST
	hasElem bool
	notNull bool
}

// parseVType parses the normalised type text produced by the syntax package
// (e.g. "LIST<INTEGER NOT NULL> NOT NULL", "INTEGER | FLOAT").
func parseVTypes(text string) []vtype {
	var out []vtype
	for _, part := range splitTop(text, '|') {
		out = append(out, parseVAtom(strings.TrimSpace(part)))
	}
	return out
}

func splitTop(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<', '(':
			depth++
		case '>', ')':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func parseVAtom(s string) vtype {
	var t vtype
	if strings.HasSuffix(s, " NOT NULL") {
		t.notNull = true
		s = strings.TrimSuffix(s, " NOT NULL")
	}
	if i := strings.IndexByte(s, '<'); i >= 0 && strings.HasSuffix(s, ">") {
		t.elem = parseVTypes(s[i+1 : len(s)-1])
		t.hasElem = true
		s = s[:i]
	}
	if i := strings.IndexByte(s, '('); i >= 0 { // VECTOR(n): dimension ignored here
		s = strings.TrimSpace(s[:i])
	}
	t.name, t.bits = canonicalTypeName(strings.TrimSpace(s))
	return t
}

func canonicalTypeName(s string) (string, int) {
	switch s {
	case "BOOLEAN", "BOOL":
		return "BOOLEAN", 0
	case "STRING", "VARCHAR":
		return "STRING", 0
	case "INTEGER", "INT", "SIGNED INTEGER", "INTEGER64", "INT64", "SIGNED INTEGER64":
		return "INTEGER", 64
	case "INTEGER32", "INT32":
		return "INTEGER", 32
	case "INTEGER16", "INT16":
		return "INTEGER", 16
	case "INTEGER8", "INT8":
		return "INTEGER", 8
	case "FLOAT", "FLOAT64", "DOUBLE", "REAL", "DOUBLE PRECISION":
		return "FLOAT", 64
	case "FLOAT32":
		return "FLOAT", 32
	case "DATE":
		return "DATE", 0
	case "LOCAL TIME", "TIME WITHOUT TIMEZONE", "TIME WITHOUT TIME ZONE":
		return "LOCAL TIME", 0
	case "ZONED TIME", "TIME", "TIME WITH TIMEZONE", "TIME WITH TIME ZONE":
		return "ZONED TIME", 0
	case "LOCAL DATETIME", "TIMESTAMP WITHOUT TIMEZONE", "TIMESTAMP WITHOUT TIME ZONE", "TIMESTAMP":
		return "LOCAL DATETIME", 0
	case "ZONED DATETIME", "DATETIME", "TIMESTAMP WITH TIMEZONE", "TIMESTAMP WITH TIME ZONE":
		return "ZONED DATETIME", 0
	case "DURATION":
		return "DURATION", 0
	case "POINT":
		return "POINT", 0
	case "NODE", "VERTEX":
		return "NODE", 0
	case "RELATIONSHIP", "EDGE":
		return "RELATIONSHIP", 0
	case "MAP":
		return "MAP", 0
	case "PATH":
		return "PATH", 0
	case "LIST", "ARRAY":
		return "LIST", 0
	case "ANY", "ANY VALUE":
		return "ANY", 0
	case "PROPERTY VALUE", "ANY PROPERTY VALUE":
		return "PROPERTY VALUE", 0
	case "NOTHING":
		return "NOTHING", 0
	case "NULL":
		return "NULL", 0
	case "VECTOR":
		return "VECTOR", 0
	}
	return s, 0
}

// matchesVTypes reports whether v is a value of any of the alternatives.
func matchesVTypes(v any, ts []vtype) bool {
	for _, t := range ts {
		if t.matches(v) {
			return true
		}
	}
	return false
}

func (t vtype) matches(v any) bool {
	if v == nil {
		return t.name == "NULL" || !t.notNull
	}
	if t.name == "NOTHING" || t.name == "NULL" {
		return false
	}
	switch t.name {
	case "ANY":
		return true
	case "BOOLEAN":
		_, ok := v.(bool)
		return ok
	case "STRING":
		_, ok := v.(string)
		return ok
	case "INTEGER":
		i, ok := v.(int64)
		if !ok {
			return false
		}
		switch t.bits {
		case 8:
			return i >= math.MinInt8 && i <= math.MaxInt8
		case 16:
			return i >= math.MinInt16 && i <= math.MaxInt16
		case 32:
			return i >= math.MinInt32 && i <= math.MaxInt32
		}
		return true
	case "FLOAT":
		f, ok := v.(float64)
		if !ok {
			return false
		}
		if t.bits == 32 && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return float64(float32(f)) == f
		}
		return true
	case "DATE", "LOCAL TIME", "ZONED TIME", "LOCAL DATETIME", "ZONED DATETIME", "DURATION":
		tv, ok := v.(temporal.Value)
		if !ok {
			return false
		}
		return temporalVType(tv) == t.name
	case "NODE":
		_, ok := v.(*Node)
		return ok
	case "RELATIONSHIP":
		_, ok := v.(*Rel)
		return ok
	case "MAP":
		_, ok := v.(map[string]any)
		return ok
	case "PATH":
		_, ok := v.(*Path)
		return ok
	case "LIST":
		l, ok := v.([]any)
		if !ok {
			return false
		}
		if !t.hasElem {
			return true
		}
		for _, e := range l {
			if !matchesVTypes(e, t.elem) {
				return false
			}
		}
		return true
	case "PROPERTY VALUE":
		return isPropertyValue(v)
	case "POINT", "VECTOR":
		return typeName(v) == titleType(t.name)
	}
	return false
}

func titleType(name string) string {
	if name == "POINT" {
		return "Point"
	}
	return "Vector"
}

func isPropertyValue(v any) bool {
	switch x := v.(type) {
	case bool, int64, float64, string, temporal.Value:
		return true
	case []any:
		var first string
		for i, e := range x {
			if e == nil || !isPropertyValue(e) {
				return false
			}
			if _, isList := e.([]any); isList {
				return false
			}
			n := valueTypeName(e, false)
			if i == 0 {
				first = n
			} else if n != first {
				return false
			}
		}
		return true
	}
	return typeName(v) == "Point" || typeName(v) == "Vector"
}

func temporalVType(v temporal.Value) string {
	switch v.Kind() {
	case temporal.KindDate:
		return "DATE"
	case temporal.KindLocalTime:
		return "LOCAL TIME"
	case temporal.KindTime:
		return "ZONED TIME"
	case temporal.KindLocalDateTime:
		return "LOCAL DATETIME"
	case temporal.KindDateTime:
		return "ZONED DATETIME"
	}
	return "DURATION"
}

// valueTypeName is the result of valueType(v): the most specific type of v,
// e.g. "INTEGER NOT NULL" or "LIST<INTEGER NOT NULL> NOT NULL". nested is true
// for list elements, which omit the trailing NOT NULL only when null occurs.
func valueTypeName(v any, withNullability bool) string {
	var base string
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		base = "BOOLEAN"
	case int64:
		base = "INTEGER"
	case float64:
		base = "FLOAT"
	case string:
		base = "STRING"
	case *Node:
		base = "NODE"
	case *Rel:
		base = "RELATIONSHIP"
	case *Path:
		base = "PATH"
	case map[string]any:
		base = "MAP"
	case temporal.Value:
		base = temporalVType(x)
	case []any:
		base = "LIST<" + listElemType(x) + ">"
	default:
		base = strings.ToUpper(typeName(v))
	}
	if withNullability {
		return base + " NOT NULL"
	}
	return base
}

// listElemType summarises the element types of a list.
func listElemType(l []any) string {
	if len(l) == 0 {
		return "NOTHING"
	}
	types := map[string]bool{}
	hasNull := false
	var first string
	for _, e := range l {
		if e == nil {
			hasNull = true
			continue
		}
		n := valueTypeName(e, false)
		if first == "" {
			first = n
		}
		types[n] = true
	}
	var inner string
	switch len(types) {
	case 0:
		return "NULL"
	case 1:
		inner = first
	default:
		inner = "ANY"
	}
	if hasNull {
		return inner
	}
	return inner + " NOT NULL"
}
