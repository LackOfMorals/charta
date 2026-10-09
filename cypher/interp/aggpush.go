package interp

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/LackOfMorals/charta/cypher/syntax"
)

// aggPushDisabled turns the aggregation pushdown off; tests use it to compare
// both plans on the same data.
var aggPushDisabled bool

// aggPushNodeProps lets a single-node aggregation that reads properties be
// pushed down too. It is off because SQLite's json functions (about 1.7 us per
// node) are no faster than the interpreter's own decoder there, so the pushdown
// only wins when it saves building nodes and relationships (a relationship
// pattern) or reading properties at all (count(*)); tests turn it on to exercise
// the shared SQL generation.
var aggPushNodeProps bool

// aggPushUsed counts how often a query ran as one SQL aggregation (for tests).
var aggPushUsed atomic.Int64

// aggItem is one output column of a pushed-down aggregation.
type aggItem struct {
	name  string
	expr  syntax.Expr
	alias string // table alias the property is read from: a, b (nodes) or e (the relationship)
	key   string // property read, for a grouping key or an aggregate argument
	fn    string // "" for a grouping key; else count, countStar, min, max, sum, avg
}

// aggregatePushdown runs `MATCH (n[:Label...]) RETURN|WITH <n.key groups and
// count/min/max/sum/avg aggregates>` as a single SQL GROUP BY instead of
// building a row, a Node and its property map for every node (a 3-million node
// aggregation spent most of its time and about 2 KB per node on that). It only
// handles the shapes whose SQL result is provably the same as the interpreter's:
// it returns handled=false (leaving st untouched) for anything else, including
// property values that are not plain strings, numbers or booleans, so the normal
// plan runs instead. The projection's DISTINCT / ORDER BY / SKIP / LIMIT / WHERE
// are applied to the grouped rows by finishProjection as usual.
func (ex *exec) aggregatePushdown(q *syntax.SingleQuery, st *qstate) (names []string, handled bool, err error) {
	if aggPushDisabled || len(q.Clauses) < 2 || len(st.rows) != 1 || len(st.rows[0]) != 0 {
		return nil, false, nil
	}
	g := ex.g
	if len(g.createdNodes)+len(g.nodeSnap)+len(g.createdRels)+len(g.relSnap)+len(g.delNodes)+len(g.delRels) != 0 {
		return nil, false, nil // earlier writes in this statement are not in SQLite's view of deletes
	}
	m, ok := q.Clauses[0].(*syntax.Match)
	if !ok || m.Optional || len(m.Patterns) != 1 {
		return nil, false, nil
	}
	pl, ok := ex.aggPattern(m.Patterns[0])
	if !ok {
		return nil, false, nil
	}
	var proj *syntax.Projection
	var where syntax.Expr
	switch c := q.Clauses[1].(type) {
	case *syntax.Return:
		proj = &c.Projection
	case *syntax.With:
		proj, where = &c.Projection, c.Where
	default:
		return nil, false, nil
	}
	if proj.Star {
		return nil, false, nil
	}
	items, ok := aggItems(proj, pl)
	if !ok {
		return nil, false, nil
	}
	if pl.rel == nil && !aggPushNodeProps {
		for _, it := range items {
			if it.key != "" {
				return nil, false, nil
			}
		}
		if m.Where != nil {
			return nil, false, nil
		}
	}

	// SELECT <key type,value>..., <aggregate, guards>... FROM ... WHERE ... GROUP BY keys
	var sel, group []string
	path := func(key string) string { return `'$."` + key + `"'` }
	for _, it := range items {
		x := it.alias + `.props, ` + path(it.key)
		switch it.fn {
		case "":
			sel = append(sel, `json_type(`+x+`)`, `json_extract(`+x+`)`)
			group = append(group, fmt.Sprint(len(sel)-1), fmt.Sprint(len(sel)))
		case "countStar":
			sel = append(sel, `COUNT(*)`)
		case "count":
			sel = append(sel, `COUNT(json_extract(`+x+`))`)
		default: // min, max, sum, avg: the value and counts of each kind of value
			sel = append(sel,
				strings.ToUpper(it.fn)+`(json_extract(`+x+`))`,
				`SUM(json_type(`+x+`) IN ('integer'))`,
				`SUM(json_type(`+x+`) IN ('real'))`,
				`SUM(json_type(`+x+`) = 'text')`,
				`SUM(json_type(`+x+`) IN ('true','false','array','object','null'))`)
		}
	}
	var conds []string
	var args []any
	// Which table aliases the query reads properties of.
	need := map[string]bool{}
	for _, it := range items {
		if it.key != "" {
			need[it.alias] = true
		}
	}
	if m.Where != nil {
		if !ex.aggWhere(m.Where, pl, &conds, &args, need) {
			return nil, false, nil
		}
	}
	var from string
	nodeID := pl.idCol("a") // the column holding the single node's id
	if pl.rel == nil {
		from = `nodes a`
		if ls := pl.labels["a"]; !need["a"] && len(ls) > 0 {
			// Only counting nodes of a label: walk the (label, node_id) index
			// without touching the nodes table (about 3x faster).
			from, nodeID = `node_labels a0`, `a0.node_id`
			conds = append(conds, `a0.label = ?`)
			args = append(args, ls[0])
			pl.labels["a"] = ls[1:]
		}
	} else {
		from = `edges e`
		for _, side := range []string{"a", "b"} {
			if need[side] {
				from += ` JOIN nodes ` + side + ` ON ` + side + `.id = ` + pl.idCol(side)
			}
		}
	}
	for _, side := range []string{"a", "b"} {
		if side == "b" && pl.rel == nil {
			continue
		}
		col := pl.idCol(side)
		if side == "a" && pl.rel == nil {
			col = nodeID
		}
		for _, l := range pl.labels[side] {
			conds = append(conds, `EXISTS (SELECT 1 FROM node_labels l WHERE l.node_id = `+col+` AND l.label = ?)`)
			args = append(args, l)
		}
	}
	if len(pl.types) > 0 {
		conds = append(conds, `e.type IN (?`+strings.Repeat(",?", len(pl.types)-1)+`)`)
		for _, t := range pl.types {
			args = append(args, t)
		}
	}
	sql := `SELECT ` + strings.Join(sel, ", ") + ` FROM ` + from
	if len(conds) > 0 {
		sql += ` WHERE ` + strings.Join(conds, " AND ")
	}
	if len(group) > 0 {
		sql += ` GROUP BY ` + strings.Join(group, ", ")
	}
	rows, err := g.db.QueryContext(g.ctx, sql, args...)
	if err != nil {
		return nil, false, nil // let the interpreter run (and report) it
	}
	defer rows.Close()

	var out, origs []row
	var substs []map[string]any
	keyKinds := map[int]map[string]bool{} // item index -> numeric/text/bool kinds seen
	for rows.Next() {
		dest := make([]any, len(sel))
		ptrs := make([]any, len(sel))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, false, nil
		}
		nr := make(row, len(items))
		subst := make(map[string]any, len(items))
		col := 0
		for idx, it := range items {
			var v any
			switch it.fn {
			case "":
				typ, _ := dest[col].(string)
				val := dest[col+1]
				col += 2
				switch {
				case dest[col-2] == nil:
					v = nil
				case typ == "integer":
					v = val
				case typ == "real":
					v = val
				case typ == "text":
					v = val
				case typ == "true":
					v = true
				case typ == "false":
					v = false
				default:
					return nil, false, nil // an array, an object (a temporal value) or a JSON null
				}
				if typ == "integer" || typ == "real" {
					if keyKinds[idx] == nil {
						keyKinds[idx] = map[string]bool{}
					}
					keyKinds[idx]["n"+typ] = true
					if keyKinds[idx]["ninteger"] && keyKinds[idx]["nreal"] {
						return nil, false, nil // 1 and 1.0 would have to share a group
					}
				}
			case "countStar", "count":
				v = dest[col]
				col++
			default:
				val := dest[col]
				nInt, nReal, nText, nOther := asInt(dest[col+1]), asInt(dest[col+2]), asInt(dest[col+3]), asInt(dest[col+4])
				col += 5
				if nOther > 0 || (nText > 0 && (nInt+nReal > 0 || it.fn == "sum" || it.fn == "avg")) {
					return nil, false, nil
				}
				if (it.fn == "sum" || it.fn == "avg") && nReal > 0 {
					return nil, false, nil // float sums depend on the order of addition
				}
				if it.fn == "sum" && val == nil {
					val = int64(0)
				}
				v = val
			}
			nr[it.name] = v
			subst[syntax.Dump(it.expr)] = v
		}
		out = append(out, nr)
		origs = append(origs, row{})
		substs = append(substs, subst)
	}
	if err := rows.Err(); err != nil {
		return nil, false, nil
	}
	aggPushUsed.Add(1)
	names = make([]string, len(items))
	for i, it := range items {
		names[i] = it.name
	}
	if err := ex.finishProjection(proj, where, st, names, true, out, origs, substs); err != nil {
		return nil, true, err
	}
	return names, true, nil
}

func asInt(v any) int64 {
	n, _ := v.(int64)
	return n
}

// aggPlan is the pattern of a pushed-down aggregation: one node, or one
// directed relationship between two nodes.
type aggPlan struct {
	vars   map[string]string   // variable -> table alias (a, b, e)
	labels map[string][]string // node alias -> required labels
	types  []string            // relationship types (any if empty)
	rel    *syntax.RelPattern
	left   bool // the pattern is (a)<-[]-(b): a is the relationship's end
}

// idCol is the edges column holding the id of the node bound to alias side.
func (pl *aggPlan) idCol(side string) string {
	if pl.rel == nil {
		return side + ".id"
	}
	if (side == "a") != pl.left {
		return "e.start_id"
	}
	return "e.end_id"
}

// aggPattern accepts `(a[:L...])` and `(a[:L...])-[e[:T|U]]->(b[:L...])` (either
// direction): no property maps, WHERE, paths, selectors or variable length.
func (ex *exec) aggPattern(part *syntax.PatternPart) (*aggPlan, bool) {
	if part.Var != "" || part.Func != syntax.FuncNone || part.Selector != nil {
		return nil, false
	}
	pl := &aggPlan{vars: map[string]string{}, labels: map[string][]string{}}
	node := func(e syntax.PatternElem, alias string) bool {
		np, ok := e.(*syntax.NodePattern)
		if !ok || np.Props != nil || np.Where != nil {
			return false
		}
		ls, ok := plainLabels(np.Labels)
		if !ok {
			return false
		}
		pl.labels[alias] = ls
		if np.Var != "" {
			if _, dup := pl.vars[np.Var]; dup {
				return false
			}
			pl.vars[np.Var] = alias
		}
		return true
	}
	switch len(part.Elems) {
	case 1:
		return pl, node(part.Elems[0], "a")
	case 3:
		rp, ok := part.Elems[1].(*syntax.RelPattern)
		if !ok || rp.Range != nil || rp.Quant != nil || rp.Props != nil || rp.Where != nil ||
			(rp.Dir != syntax.DirRight && rp.Dir != syntax.DirLeft) {
			return nil, false
		}
		pl.rel, pl.left = rp, rp.Dir == syntax.DirLeft
		if !node(part.Elems[0], "a") || !node(part.Elems[2], "b") {
			return nil, false
		}
		if rp.Var != "" {
			if _, dup := pl.vars[rp.Var]; dup {
				return nil, false
			}
			pl.vars[rp.Var] = "e"
		}
		types, ok := plainTypes(rp.Types)
		pl.types = types
		return pl, ok
	}
	return nil, false
}

// plainTypes returns the relationship types of a `:A|B` expression (none for no
// type).
func plainTypes(le syntax.LabelExpr) ([]string, bool) {
	switch le := le.(type) {
	case nil:
		return nil, true
	case *syntax.LabelName:
		if le.Dynamic != nil || le.Name == "" {
			return nil, false
		}
		return []string{le.Name}, true
	case *syntax.LabelOr:
		l, ok := plainTypes(le.L)
		if !ok {
			return nil, false
		}
		r, ok := plainTypes(le.R)
		return append(l, r...), ok && len(l) > 0 && len(r) > 0
	}
	return nil, false
}

// aggWhere translates a MATCH's WHERE made of AND-ed comparisons between a
// property and a literal or parameter (= < <= > >=) into SQL. A property of
// another kind than the value compares as null in Cypher and so never matches;
// the json_type guard reproduces that.
func (ex *exec) aggWhere(e syntax.Expr, pl *aggPlan, conds *[]string, args *[]any, need map[string]bool) bool {
	switch e := e.(type) {
	case *syntax.Binary:
		return e.Op == syntax.OpAnd && ex.aggWhere(e.L, pl, conds, args, need) && ex.aggWhere(e.R, pl, conds, args, need)
	case *syntax.Comparison:
		if len(e.Ops) != 1 {
			return false
		}
		op := e.Ops[0]
		sqlOp := map[syntax.CompareOp]string{syntax.CmpEq: "=", syntax.CmpLt: "<", syntax.CmpLte: "<=", syntax.CmpGt: ">", syntax.CmpGte: ">="}[op]
		if sqlOp == "" {
			return false
		}
		prop, val := e.Operands[0], e.Operands[1]
		if _, ok := prop.(*syntax.Property); !ok {
			prop, val = val, prop
			sqlOp = map[string]string{"=": "=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}[sqlOp]
		}
		pr, ok := prop.(*syntax.Property)
		if !ok || !pushableKey(pr.Key) {
			return false
		}
		id, ok := pr.Subject.(*syntax.Ident)
		if !ok {
			return false
		}
		alias, ok := pl.vars[id.Name]
		if !ok {
			return false
		}
		switch val.(type) {
		case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.Param:
		default:
			return false
		}
		v, err := ex.eval(val, row{})
		if err != nil {
			return false
		}
		x := alias + `.props, '$."` + pr.Key + `"'`
		switch v := v.(type) {
		case int64, float64:
			if f, isF := v.(float64); isF && f != f {
				return false
			}
			*conds = append(*conds, `(json_type(`+x+`) IN ('integer','real') AND json_extract(`+x+`) `+sqlOp+` ?)`)
		case string:
			*conds = append(*conds, `(json_type(`+x+`) = 'text' AND json_extract(`+x+`) `+sqlOp+` ?)`)
		default:
			return false
		}
		*args = append(*args, v)
		need[alias] = true
		return true
	}
	return false
}

// plainLabels returns the labels of a node pattern that is a conjunction of
// plain label names (or no label at all).
func plainLabels(le syntax.LabelExpr) ([]string, bool) {
	switch le := le.(type) {
	case nil:
		return nil, true
	case *syntax.LabelName:
		if le.Dynamic != nil || le.Name == "" {
			return nil, false
		}
		return []string{le.Name}, true
	case *syntax.LabelAnd:
		l, ok := plainLabels(le.L)
		if !ok {
			return nil, false
		}
		r, ok := plainLabels(le.R)
		return append(l, r...), ok
	}
	return nil, false
}

// aggItems classifies the projection's items: grouping keys of the form var.key
// and the aggregates count(*), count(var), count(var.key), min/max/sum/avg(var.key)
// over the pattern's variables. At least one aggregate is required.
func aggItems(p *syntax.Projection, pl *aggPlan) ([]aggItem, bool) {
	var items []aggItem
	hasAgg := false
	prop := func(e syntax.Expr) (alias, key string, ok bool) {
		pr, isProp := e.(*syntax.Property)
		if !isProp {
			return "", "", false
		}
		id, isID := pr.Subject.(*syntax.Ident)
		if !isID || !pushableKey(pr.Key) {
			return "", "", false
		}
		alias, ok = pl.vars[id.Name]
		return alias, pr.Key, ok
	}
	for _, it := range p.Items {
		name := it.Alias
		if name == "" {
			name = it.Source
		}
		if _, isProp := it.Expr.(*syntax.Property); isProp {
			alias, key, ok := prop(it.Expr)
			if !ok {
				return nil, false
			}
			items = append(items, aggItem{name: name, expr: it.Expr, alias: alias, key: key})
			continue
		}
		f, ok := it.Expr.(*syntax.FuncCall)
		if !ok || len(f.Namespace) != 0 || f.Distinct {
			return nil, false
		}
		hasAgg = true
		fn := strings.ToLower(f.Name)
		switch {
		case fn == "count" && f.Star:
			items = append(items, aggItem{name: name, expr: it.Expr, fn: "countStar"})
		case fn == "count" && len(f.Args) == 1:
			if id, isID := f.Args[0].(*syntax.Ident); isID {
				if _, bound := pl.vars[id.Name]; !bound {
					return nil, false
				}
				items = append(items, aggItem{name: name, expr: it.Expr, fn: "countStar"})
				continue
			}
			alias, key, ok := prop(f.Args[0])
			if !ok {
				return nil, false
			}
			items = append(items, aggItem{name: name, expr: it.Expr, alias: alias, key: key, fn: "count"})
		case (fn == "min" || fn == "max" || fn == "sum" || fn == "avg") && len(f.Args) == 1:
			alias, key, ok := prop(f.Args[0])
			if !ok {
				return nil, false
			}
			items = append(items, aggItem{name: name, expr: it.Expr, alias: alias, key: key, fn: fn})
		default:
			return nil, false
		}
	}
	return items, hasAgg && len(items) > 0
}
