//go:build tck

package compat

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	graphlite "github.com/LackOfMorals/graphlite/v2"
	"github.com/LackOfMorals/graphlite/v2/cypher/spatial"
	"github.com/LackOfMorals/graphlite/v2/cypher/temporal"
	"github.com/LackOfMorals/graphlite/v2/cypher/vector"
)

// TCK result tables write values in a Cypher-like notation: 1, 1.5, 'text',
// [1, 2], {k: v}, (:Label {k: v}), [:TYPE {k: v}] and <(a)-[:T]->(b)> for
// paths. This file parses that notation and converts actual query results into
// the same structures so they can be compared structurally.

type (
	tvNode struct {
		labels []string
		props  map[string]any
	}
	tvRel struct {
		typ   string
		props map[string]any
	}
	tvPath struct {
		nodes []*tvNode
		rels  []*tvRel
		dirs  []int // +1 for ->, -1 for <-
	}
)

type tvParser struct {
	s string
	i int
}

func parseTV(s string) (any, error) {
	p := &tvParser{s: strings.TrimSpace(s)}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("unexpected %q at %d in %q", p.s[p.i:], p.i, p.s)
	}
	return v, nil
}

func (p *tvParser) ws() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *tvParser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *tvParser) has(prefix string) bool { return strings.HasPrefix(p.s[p.i:], prefix) }

func (p *tvParser) value() (any, error) {
	p.ws()
	switch c := p.peek(); {
	case c == '\'':
		return p.str()
	case c == '[':
		if p.has("[:") || p.has("[]") && false {
			return p.rel()
		}
		return p.list()
	case c == '{':
		return p.mapLit()
	case c == '(':
		return p.node()
	case c == '<':
		return p.path()
	}
	for _, kw := range []struct {
		text string
		val  any
	}{{"null", nil}, {"true", true}, {"false", false}, {"NaN", math.NaN()}, {"-Infinity", math.Inf(-1)}, {"Infinity", math.Inf(1)}} {
		if p.has(kw.text) {
			p.i += len(kw.text)
			return kw.val, nil
		}
	}
	return p.number()
}

func (p *tvParser) number() (any, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || strings.IndexByte(".eE+-", p.s[p.i]) >= 0) {
		if (p.s[p.i] == '+' || p.s[p.i] == '-') && !(p.s[p.i-1] == 'e' || p.s[p.i-1] == 'E') {
			break
		}
		p.i++
	}
	text := p.s[start:p.i]
	if text == "" {
		return nil, fmt.Errorf("unexpected %q in %q", p.s[p.i:], p.s)
	}
	if !strings.ContainsAny(text, ".eE") {
		if v, err := strconv.ParseInt(text, 10, 64); err == nil {
			return v, nil
		}
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, fmt.Errorf("bad number %q", text)
	}
	return v, nil
}

func (p *tvParser) str() (any, error) {
	p.i++ // opening quote
	var sb strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '\\' && p.i+1 < len(p.s):
			p.i++
			switch p.s[p.i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'u':
				if p.i+4 < len(p.s) {
					if n, err := strconv.ParseUint(p.s[p.i+1:p.i+5], 16, 32); err == nil {
						sb.WriteRune(rune(n))
						p.i += 4
					}
				}
			default:
				sb.WriteByte(p.s[p.i])
			}
			p.i++
		case c == '\'':
			p.i++
			return sb.String(), nil
		default:
			sb.WriteByte(c)
			p.i++
		}
	}
	return nil, fmt.Errorf("unterminated string in %q", p.s)
}

func (p *tvParser) list() (any, error) {
	p.i++ // [
	out := []any{}
	for {
		p.ws()
		if p.peek() == ']' {
			p.i++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.ws()
		if p.peek() == ',' {
			p.i++
		}
	}
}

func (p *tvParser) ident() string {
	p.ws()
	if p.peek() == '`' {
		p.i++
		start := p.i
		for p.i < len(p.s) && p.s[p.i] != '`' {
			p.i++
		}
		id := p.s[start:p.i]
		p.i++
		return id
	}
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] == '_' || p.s[p.i] >= 'a' && p.s[p.i] <= 'z' || p.s[p.i] >= 'A' && p.s[p.i] <= 'Z' || p.s[p.i] >= '0' && p.s[p.i] <= '9') {
		p.i++
	}
	return p.s[start:p.i]
}

func (p *tvParser) mapLit() (map[string]any, error) {
	p.i++ // {
	out := map[string]any{}
	for {
		p.ws()
		if p.peek() == '}' {
			p.i++
			return out, nil
		}
		key := p.ident()
		p.ws()
		if p.peek() != ':' {
			return nil, fmt.Errorf("expected ':' after key %q in %q", key, p.s)
		}
		p.i++
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out[key] = v
		p.ws()
		if p.peek() == ',' {
			p.i++
		}
	}
}

func (p *tvParser) node() (*tvNode, error) {
	p.i++ // (
	n := &tvNode{props: map[string]any{}}
	p.ws()
	for p.peek() == ':' {
		p.i++
		n.labels = append(n.labels, p.ident())
		p.ws()
	}
	if p.peek() == '{' {
		m, err := p.mapLit()
		if err != nil {
			return nil, err
		}
		n.props = m
		p.ws()
	}
	if p.peek() != ')' {
		return nil, fmt.Errorf("expected ')' in node literal %q", p.s)
	}
	p.i++
	sort.Strings(n.labels)
	return n, nil
}

func (p *tvParser) rel() (*tvRel, error) {
	p.i++ // [
	r := &tvRel{props: map[string]any{}}
	p.ws()
	if p.peek() == ':' {
		p.i++
		r.typ = p.ident()
		p.ws()
	}
	if p.peek() == '{' {
		m, err := p.mapLit()
		if err != nil {
			return nil, err
		}
		r.props = m
		p.ws()
	}
	if p.peek() != ']' {
		return nil, fmt.Errorf("expected ']' in relationship literal %q", p.s)
	}
	p.i++
	return r, nil
}

func (p *tvParser) path() (*tvPath, error) {
	p.i++ // <
	path := &tvPath{}
	first, err := p.node()
	if err != nil {
		return nil, err
	}
	path.nodes = append(path.nodes, first)
	for {
		p.ws()
		if p.peek() == '>' {
			p.i++
			return path, nil
		}
		dir := 1
		switch {
		case p.has("<-"):
			dir = -1
			p.i += 2
		case p.has("-"):
			p.i++
		default:
			return nil, fmt.Errorf("expected a relationship in path %q", p.s)
		}
		if p.peek() != '[' {
			return nil, fmt.Errorf("expected '[' in path %q", p.s)
		}
		r, err := p.rel()
		if err != nil {
			return nil, err
		}
		switch {
		case p.has("->"):
			p.i += 2
		case p.has("-"):
			p.i++
		}
		n, err := p.node()
		if err != nil {
			return nil, err
		}
		path.rels = append(path.rels, r)
		path.dirs = append(path.dirs, dir)
		path.nodes = append(path.nodes, n)
	}
}

// ─── actual values ───────────────────────────────────────────────────────────

// fromActual converts a query result value into the structures parseTV
// produces. When lenient, integral floats count as integers and 0/1 as
// booleans (the SQL engine returns JSON-decoded numbers).
func fromActual(v any, lenient bool) any {
	switch x := v.(type) {
	case nil, bool, string, int64:
		return x
	case temporal.Value:
		return x.String() // the TCK writes temporal values as their string form
	case spatial.Point:
		return x.String()
	case vector.Vector:
		return x.String()
	case int:
		return int64(x)
	case float64:
		if lenient && x == math.Trunc(x) && math.Abs(x) < 9e18 {
			return int64(x)
		}
		return x
	case *graphlite.Node:
		return actualNode(x, lenient)
	case graphlite.Node:
		return actualNode(&x, lenient)
	case *graphlite.Relationship:
		return actualRel(x, lenient)
	case graphlite.Relationship:
		return actualRel(&x, lenient)
	case graphlite.Path:
		p := &tvPath{}
		for _, n := range x.Nodes {
			n := n
			p.nodes = append(p.nodes, actualNode(&n, lenient))
		}
		for i, r := range x.Relationships {
			r := r
			p.rels = append(p.rels, actualRel(&r, lenient))
			dir := 1
			if i < len(x.Nodes) && r.StartElementId != x.Nodes[i].ElementId {
				dir = -1
			}
			p.dirs = append(p.dirs, dir)
		}
		return p
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fromActual(e, lenient)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = fromActual(e, lenient)
		}
		return out
	}
	return fmt.Sprintf("<unsupported %T: %v>", v, v)
}

func actualNode(n *graphlite.Node, lenient bool) *tvNode {
	out := &tvNode{labels: append([]string(nil), n.Labels...), props: map[string]any{}}
	sort.Strings(out.labels)
	for k, v := range n.Props {
		out.props[k] = fromActual(v, lenient)
	}
	return out
}

func actualRel(r *graphlite.Relationship, lenient bool) *tvRel {
	out := &tvRel{typ: r.Type, props: map[string]any{}}
	for k, v := range r.Props {
		out.props[k] = fromActual(v, lenient)
	}
	return out
}

// ─── canonical keys and comparison ───────────────────────────────────────────

// tvKey renders a value canonically. When ignoreListOrder is set, list
// elements are sorted (the TCK's "ignoring element order for lists").
func tvKey(v any, ignoreListOrder bool) string {
	var sb strings.Builder
	writeTV(&sb, v, ignoreListOrder)
	return sb.String()
}

func writeTV(sb *strings.Builder, v any, io bool) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		sb.WriteString(strconv.FormatBool(x))
	case int64:
		sb.WriteString("i" + strconv.FormatInt(x, 10))
	case float64:
		switch {
		case math.IsNaN(x):
			sb.WriteString("fNaN")
		case x == 0:
			sb.WriteString("f0") // -0.0 equals 0.0
		default:
			sb.WriteString("f" + strconv.FormatFloat(x, 'g', -1, 64))
		}
	case string:
		sb.WriteString(strconv.Quote(x))
	case []any:
		keys := make([]string, len(x))
		for i, e := range x {
			keys[i] = tvKey(e, io)
		}
		if io {
			sort.Strings(keys)
		}
		sb.WriteString("[" + strings.Join(keys, ",") + "]")
	case map[string]any:
		sb.WriteString("{")
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(strconv.Quote(k) + ":")
			writeTV(sb, x[k], io)
			sb.WriteString(",")
		}
		sb.WriteString("}")
	case *tvNode:
		sb.WriteString("(:" + strings.Join(x.labels, ":"))
		writeTV(sb, x.props, io)
		sb.WriteString(")")
	case *tvRel:
		sb.WriteString("[:" + x.typ)
		writeTV(sb, x.props, io)
		sb.WriteString("]")
	case *tvPath:
		sb.WriteString("<")
		for i, n := range x.nodes {
			writeTV(sb, n, io)
			if i < len(x.rels) {
				if x.dirs[i] < 0 {
					sb.WriteString("<-")
				} else {
					sb.WriteString("-")
				}
				writeTV(sb, x.rels[i], io)
				if x.dirs[i] > 0 {
					sb.WriteString("->")
				} else {
					sb.WriteString("-")
				}
			}
		}
		sb.WriteString(">")
	default:
		fmt.Fprintf(sb, "?%v", x)
	}
}
