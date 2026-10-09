package interp

import (
	"strings"
	"sync/atomic"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// relFirstDisabled turns the relationship-first strategy off; tests use it to
// compare both strategies on the same data.
var relFirstDisabled bool

// relFirstUsed counts how often the strategy was chosen (for tests).
var relFirstUsed atomic.Int64

// scanRels returns the live relationships of the given type (any type if typ is
// empty) whose properties equal the hints, in id order. As with scanNodes the
// hints only let SQLite skip rows that cannot match: the caller re-checks every
// candidate.
func (g *graph) scanRels(typ string, hints []propHint) ([]*Rel, error) {
	var sb strings.Builder
	var args []any
	sb.WriteString(`SELECT e.id, e.type, e.start_id, e.end_id, e.props FROM edges e WHERE 1`)
	if typ != "" {
		sb.WriteString(` AND e.type = ?`)
		args = append(args, typ)
	}
	for _, h := range hints {
		// Inlined (h.key is a plain identifier) so an expression index on
		// json_extract(props, '$."key"') can serve it.
		sb.WriteString(` AND json_extract(e.props, '$."` + h.key + `"') = ?`)
		args = append(args, h.val)
	}
	sb.WriteString(` ORDER BY e.id`)
	rows, err := g.db.QueryContext(g.ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	type rec struct {
		id         int64
		typ        string
		start, end int64
		props      string
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.typ, &r.start, &r.end, &r.props); err != nil {
			rows.Close()
			return nil, err
		}
		recs = append(recs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]*Rel, 0, len(recs))
	for _, r := range recs {
		rel, err := g.internRel(r.id, r.typ, r.start, r.end, r.props)
		if err != nil {
			return nil, err
		}
		if !rel.Deleted {
			out = append(out, rel)
		}
	}
	return out, nil
}

// relHints collects the property equalities that narrow a relationship pattern,
// from its inline property map and the enclosing MATCH's WHERE.
func (ex *exec) relHints(rp *syntax.RelPattern, r row) []propHint {
	var hints []propHint
	add := func(key string, e syntax.Expr) {
		if v, ok := ex.hintValue(e, r); ok && pushableKey(key) {
			hints = append(hints, propHint{key, v})
		}
	}
	if p, ok := rp.Props.(*syntax.MapLit); ok {
		for _, en := range p.Entries {
			add(en.Key, en.Value)
		}
	}
	if rp.Var != "" {
		for _, pe := range ex.pushed {
			if pe.variable == rp.Var {
				add(pe.key, pe.val)
			}
		}
	}
	return hints
}

// matchRelFirst matches (a)-[r {k: v}]-(b) by looking the relationship up by
// its property (using an index if there is one) instead of scanning every node
// and walking its relationships. It applies when the relationship pattern has
// property equalities and neither end is bound or constrained by properties, so
// the node-first plan would have scanned the whole node table. handled is false
// when the pattern does not qualify.
func (ex *exec) matchRelFirst(part *syntax.PatternPart, r row, used map[int64]bool, finish func(row, *pathState) error) (handled bool, err error) {
	if relFirstDisabled || len(part.Elems) != 3 {
		return false, nil
	}
	a, ok1 := part.Elems[0].(*syntax.NodePattern)
	rp, ok2 := part.Elems[1].(*syntax.RelPattern)
	b, ok3 := part.Elems[2].(*syntax.NodePattern)
	if !ok1 || !ok2 || !ok3 || rangeOf(rp) != nil {
		return false, nil
	}
	for _, np := range []*syntax.NodePattern{a, b} {
		if np.Props != nil || np.Where != nil {
			return false, nil
		}
		if np.Var != "" {
			if _, bound := r[np.Var]; bound {
				return false, nil
			}
		}
	}
	if rp.Var != "" {
		if _, bound := r[rp.Var]; bound {
			return false, nil
		}
	}
	hints := ex.relHints(rp, r)
	if len(hints) == 0 {
		return false, nil
	}
	typ := ""
	if ln, ok := rp.Types.(*syntax.LabelName); ok && ln.Dynamic == nil {
		typ = ln.Name
	}
	relFirstUsed.Add(1)
	rels, err := ex.g.scanRels(typ, hints)
	if err != nil {
		return true, err
	}
	for _, rel := range rels {
		if used[rel.ID] {
			continue
		}
		if ok, err := ex.relMatches(rp, rel, r); err != nil || !ok {
			if err != nil {
				return true, err
			}
			continue
		}
		// The orientations the pattern allows: (a)-[]->(b) reads the relationship
		// forwards, (a)<-[]-(b) backwards, an undirected one both ways (a
		// self-loop only once).
		type orient struct{ from, to int64 }
		var os []orient
		if rp.Dir != syntax.DirLeft {
			os = append(os, orient{rel.Start, rel.End})
		}
		if rp.Dir != syntax.DirRight && !(rp.Dir != syntax.DirLeft && rel.Start == rel.End) {
			os = append(os, orient{rel.End, rel.Start})
		}
		for _, o := range os {
			from, err := ex.g.node(o.from)
			if err != nil || from.Deleted {
				if err != nil {
					return true, err
				}
				continue
			}
			to, err := ex.g.node(o.to)
			if err != nil || to.Deleted {
				if err != nil {
					return true, err
				}
				continue
			}
			ok, err := ex.nodeMatches(a, from, r)
			if err != nil {
				return true, err
			}
			if !ok {
				continue
			}
			r2 := r
			if a.Var != "" {
				r2 = r2.with(a.Var, from)
			}
			r3, ok, err := ex.bindStep(rp, rel, b, to, r2)
			if err != nil {
				return true, err
			}
			if !ok {
				continue
			}
			used[rel.ID] = true
			err = finish(r3, &pathState{nodes: []*Node{from, to}, rels: []*Rel{rel}})
			delete(used, rel.ID)
			if err != nil {
				return true, err
			}
		}
	}
	return true, nil
}
