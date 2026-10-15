package interp

import (
	"github.com/graingraph/graingraph/cypher/syntax"
	"math"
	"sync/atomic"
)

// matchParts calls emit once for every way of matching all parts against the
// graph, extending base. Within one call a relationship is matched at most once
// (Cypher's relationship isomorphism); bound variables must match their values.
func (ex *exec) matchParts(parts []*syntax.PatternPart, base row, emit func(row) error) error {
	// A nested pattern (WHERE EXISTS {...}) is not subject to an enclosing selector's length limit.
	if ex.relLimit > 0 {
		saved, pruned := ex.relLimit, ex.relPruned
		ex.relLimit = 0
		defer func() { ex.relLimit, ex.relPruned = saved, pruned }()
	}
	used := map[int64]bool{}
	var rec func(i int, r row) error
	rec = func(i int, r row) error {
		if i == len(parts) {
			return emit(r)
		}
		return ex.matchPart(parts[i], r, used, func(r2 row) error { return rec(i+1, r2) })
	}
	return rec(0, base)
}

// rangeOf returns a relationship pattern's hop range: its explicit *min..max
// range or the equivalent of a quantifier suffix (->+, ->{1,3}); nil for a
// single hop.
func rangeOf(rp *syntax.RelPattern) *syntax.Range {
	if rp.Range != nil {
		return rp.Range
	}
	if rp.Quant != nil {
		min := rp.Quant.Min
		return &syntax.Range{Min: &min, Max: rp.Quant.Max}
	}
	return nil
}

// pathState accumulates a path while a pattern is walked.
type pathState struct {
	nodes []*Node
	rels  []*Rel
}

func (ex *exec) matchPart(part *syntax.PatternPart, r row, used map[int64]bool, cont func(row) error) error {
	if part.Func != syntax.FuncNone || part.Selector != nil {
		if ex.bfsSelectable(part) {
			return ex.matchShortest(part, r, used, cont)
		}
		return ex.matchSelected(part, r, used, cont)
	}
	finish := func(r2 row, ps *pathState) error {
		if part.Var != "" {
			p := &Path{Nodes: append([]*Node{}, ps.nodes...), Rels: append([]*Rel{}, ps.rels...)}
			r2 = r2.with(part.Var, p)
		}
		return cont(r2)
	}
	if g, ok := part.Elems[0].(*syntax.GroupPattern); ok {
		// The pattern starts with a quantified group: any node may begin it, and
		// the group's own first node pattern decides.
		var label string
		if np, ok := firstNode(g).(*syntax.NodePattern); ok {
			label = scanLabel(np.Labels)
		}
		cands, err := ex.g.scanNodes(label, nil)
		if err != nil {
			return err
		}
		for _, n := range cands {
			if err := ex.walkGroup(part.Elems, 0, g, n, r, &pathState{nodes: []*Node{n}}, used, finish); err != nil {
				return err
			}
		}
		return nil
	}
	if handled, err := ex.matchRelFirst(part, r, used, finish); handled || err != nil {
		return err
	}
	if elems, ok := ex.reversed(part, r); ok {
		reverseUsed.Add(1)
		return ex.forEachStart(elems[0].(*syntax.NodePattern), restOf(elems), r, func(n *Node, r2 row) error {
			return ex.walk(elems, 1, n, r2, &pathState{nodes: []*Node{n}}, used, finish)
		})
	}
	first := part.Elems[0].(*syntax.NodePattern)
	return ex.forEachStart(first, restOf(part.Elems), r, func(n *Node, r2 row) error {
		return ex.walk(part.Elems, 1, n, r2, &pathState{nodes: []*Node{n}}, used, finish)
	})
}

// firstNode is the first element of a group pattern.
func firstNode(g *syntax.GroupPattern) syntax.PatternElem {
	if len(g.Elems) == 0 {
		return nil
	}
	return g.Elems[0]
}

// startChunk is how many candidate start nodes forEachStart filters and
// prefetches at a time. It bounds the work done ahead of the consumer, so a
// query that stops early (LIMIT) does not load the adjacency of every node.
const startChunk = 512

// prefetchDisabled turns off prefetching along a pattern from its start nodes;
// tests use it to compare both ways on the same data.
var prefetchDisabled bool

// prefetchUsed counts how often a chunk of start nodes was prefetched (for tests).
var prefetchUsed atomic.Int64

// maxPrefetchFrontier bounds how many nodes beyond the start nodes are loaded
// ahead of the walk. Past it the walk falls back to loading lazily, so a
// query that stops early (LIMIT) on a high fan-out graph does not preload a
// large part of the database.
const maxPrefetchFrontier = 4096

// restOf returns the pattern elements after the first node.
func restOf(elems []syntax.PatternElem) []syntax.PatternElem {
	if len(elems) > 1 {
		return elems[1:]
	}
	return nil
}

// forEachStart calls fn for every node that can start the pattern. rest is the
// remainder of the path pattern (relationship, node, relationship, ...); it
// lets the adjacency along the pattern be loaded for a chunk of start nodes in
// a few batched queries instead of one per node.
func (ex *exec) forEachStart(np *syntax.NodePattern, rest []syntax.PatternElem, r row, fn func(*Node, row) error) error {
	if np.Var != "" {
		if v, bound := r[np.Var]; bound {
			n, ok := v.(*Node)
			if !ok || n.Deleted {
				return nil
			}
			ok, err := ex.nodeMatches(np, n, r)
			if err != nil || !ok {
				return err
			}
			return fn(n, r)
		}
	}
	// visit binds a matching start node and runs the rest of the pattern.
	visit := func(n *Node) error {
		r2 := r
		if np.Var != "" {
			r2 = r.with(np.Var, n)
		}
		if np.Where != nil {
			c, err := ex.eval(np.Where, r2)
			if err != nil {
				return err
			}
			if c != true {
				return nil
			}
		}
		return fn(n, r2)
	}
	return ex.eachStartChunk(scanLabel(np.Labels), ex.scanHints(np, r), func(cands []*Node) error {
		matched := make([]*Node, 0, len(cands))
		for _, n := range cands {
			ok, err := ex.nodeMatches(np, n, r)
			if err != nil {
				return err
			}
			if ok {
				matched = append(matched, n)
			}
		}
		// Only nodes that will be walked from are prefetched.
		if len(rest) > 0 && !prefetchDisabled {
			if err := ex.prefetchChain(rest, matched, r); err != nil {
				return err
			}
		}
		for _, n := range matched {
			if err := visit(n); err != nil {
				return err
			}
		}
		return nil
	})
}

// scanPageSize is the most nodes a streaming scan reads per statement. The first
// page is one chunk, so a query that stops early reads little; later pages are
// larger, so a long scan does not pay for a statement every startChunk rows.
const scanPageSize = 4 * startChunk

// eachStartChunk calls fn with the candidate start nodes in chunks of
// startChunk, in id order. Inside a streaming pipeline a scan that only
// narrows by label or property equalities is read a page at a time, so a query
// that stops early (a LIMIT) reads the first page, not everything that
// matches. A page resumes after the last id of the one before, so the scan as a
// whole is a single pass. (Nothing writes while a pipeline runs, so pages see a stable graph.)
// When the statement only reads, the caches are trimmed between pages so a long
// scan does not keep every node it has visited.
func (ex *exec) eachStartChunk(label string, hints []propHint, fn func([]*Node) error) error {
	g := ex.g
	if ex.streaming > 0 {
		for _, h := range hints {
			g.eng.noteScan(g.ctx, g.db, h.key, g.readOnly)
		}
		ex.scanDepth++
		defer func() { ex.scanDepth-- }()
		after := int64(math.MinInt64)
		size := startChunk
		for {
			if ex.scanDepth == 1 { // the outermost scan; inner ones run inside its chunk
				g.trimCaches()
			}
			page, last, raw, err := g.scanPage(label, hints, after, size)
			if err != nil {
				return err
			}
			for lo := 0; lo < len(page); lo += startChunk {
				if err := fn(page[lo:min(lo+startChunk, len(page))]); err != nil {
					return err
				}
			}
			if raw < size {
				return nil
			}
			after = last
			size = scanPageSize
		}
	}
	cands, err := g.scanNodes(label, hints)
	if err != nil {
		return err
	}
	for lo := 0; lo < len(cands); lo += startChunk {
		if err := fn(cands[lo:min(lo+startChunk, len(cands))]); err != nil {
			return err
		}
	}
	return nil
}

// scanLabel picks a label to narrow a node scan: the first plain label of a
// conjunction, or none.
func scanLabel(le syntax.LabelExpr) string {
	switch le := le.(type) {
	case *syntax.LabelName:
		if le.Dynamic == nil {
			return le.Name
		}
	case *syntax.LabelAnd:
		if l := scanLabel(le.L); l != "" {
			return l
		}
		return scanLabel(le.R)
	}
	return ""
}

// nodeMatches checks a node against a node pattern's labels and property map
// (not its inline WHERE, which needs the variable bound).
func (ex *exec) nodeMatches(np *syntax.NodePattern, n *Node, r row) (bool, error) {
	if ok, err := ex.matchLabelExpr(np.Labels, n.Labels, r); err != nil || !ok {
		return false, err
	}
	return ex.propsMatch(np.Props, n.Props, r)
}

func (ex *exec) propsMatch(spec syntax.Expr, props map[string]any, r row) (bool, error) {
	switch p := spec.(type) {
	case nil:
		return true, nil
	case *syntax.MapLit:
		for _, en := range p.Entries {
			want, err := ex.eval(en.Value, r)
			if err != nil {
				return false, err
			}
			if equals(props[en.Key], want) != triTrue {
				return false, nil
			}
		}
		return true, nil
	case *syntax.Param:
		v, err := ex.eval(p, r)
		if err != nil {
			return false, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return false, typeErr("a property map parameter must be a map, got %s", typeName(v))
		}
		for k, want := range m {
			if equals(props[k], want) != triTrue {
				return false, nil
			}
		}
		return true, nil
	}
	return false, unsupported("property specification %T", spec)
}

// walk matches the relationship/node pairs of a pattern from elems[i].
func (ex *exec) walk(elems []syntax.PatternElem, i int, cur *Node, r row, ps *pathState, used map[int64]bool, done func(row, *pathState) error) error {
	if i >= len(elems) {
		return done(r, ps)
	}
	if g, ok := elems[i].(*syntax.GroupPattern); ok {
		return ex.walkGroup(elems, i, g, cur, r, ps, used, done)
	}
	rp := elems[i].(*syntax.RelPattern)
	if ex.overBudget(len(ps.rels)) {
		return nil
	}
	if g, ok := elems[i+1].(*syntax.GroupPattern); ok {
		// A relationship leading into a quantified group: the node it reaches
		// is the group's first node.
		if rangeOf(rp) != nil {
			return unsupported("a variable-length relationship directly before a quantified path pattern")
		}
		return ex.expand(rp, cur, r, used, func(rel *Rel, other *Node) error {
			r2, ok, err := ex.bindStep(rp, rel, &syntax.NodePattern{}, other, r)
			if err != nil || !ok {
				return err
			}
			used[rel.ID] = true
			ps.nodes = append(ps.nodes, other)
			ps.rels = append(ps.rels, rel)
			err = ex.walkGroup(elems, i+1, g, other, r2, ps, used, done)
			ps.nodes = ps.nodes[:len(ps.nodes)-1]
			ps.rels = ps.rels[:len(ps.rels)-1]
			delete(used, rel.ID)
			return err
		})
	}
	next := elems[i+1].(*syntax.NodePattern)
	if rangeOf(rp) != nil {
		return ex.walkVarLength(elems, i, rp, next, cur, r, ps, used, done)
	}
	return ex.expand(rp, cur, r, used, func(rel *Rel, other *Node) error {
		r2, ok, err := ex.bindStep(rp, rel, next, other, r)
		if err != nil || !ok {
			return err
		}
		used[rel.ID] = true
		ps.nodes = append(ps.nodes, other)
		ps.rels = append(ps.rels, rel)
		err = ex.walk(elems, i+2, other, r2, ps, used, done)
		ps.nodes = ps.nodes[:len(ps.nodes)-1]
		ps.rels = ps.rels[:len(ps.rels)-1]
		delete(used, rel.ID)
		return err
	})
}

// expand calls fn for each relationship leaving cur in the pattern's direction
// that satisfies its type and property constraints, with the node at the other
// end. Undirected patterns see a self-loop once.
func (ex *exec) expand(rp *syntax.RelPattern, cur *Node, r row, used map[int64]bool, fn func(*Rel, *Node) error) error {
	try := func(rel *Rel, otherID int64) error {
		if used[rel.ID] || rel.Deleted {
			return nil
		}
		if ok, err := ex.relMatches(rp, rel, r); err != nil || !ok {
			return err
		}
		other, err := ex.g.node(otherID)
		if err != nil {
			return err
		}
		if other.Deleted {
			return nil
		}
		return fn(rel, other)
	}
	if rp.Dir != syntax.DirLeft {
		rels, err := ex.g.outRels(cur.ID)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			if err := try(rel, rel.End); err != nil {
				return err
			}
		}
	}
	if rp.Dir != syntax.DirRight {
		rels, err := ex.g.inRels(cur.ID)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			if rp.Dir != syntax.DirLeft && rel.Start == rel.End {
				continue // a self-loop was already offered as outgoing
			}
			if err := try(rel, rel.Start); err != nil {
				return err
			}
		}
	}
	return nil
}

// relMatches checks a relationship against a pattern's types and properties.
func (ex *exec) relMatches(rp *syntax.RelPattern, rel *Rel, r row) (bool, error) {
	if rp.Types != nil {
		ok, err := ex.matchLabelExpr(rp.Types, []string{rel.Type}, r)
		if err != nil || !ok {
			return false, err
		}
	}
	return ex.propsMatch(rp.Props, rel.Props, r)
}

// bindStep binds a matched relationship and its far node into the row, checking
// already-bound variables and the node pattern.
func (ex *exec) bindStep(rp *syntax.RelPattern, rel *Rel, np *syntax.NodePattern, other *Node, r row) (row, bool, error) {
	if rp.Var != "" {
		if v, bound := r[rp.Var]; bound {
			b, ok := v.(*Rel)
			if !ok || b.ID != rel.ID {
				return nil, false, nil
			}
		}
	}
	if np.Var != "" {
		if v, bound := r[np.Var]; bound {
			b, ok := v.(*Node)
			if !ok || b.ID != other.ID {
				return nil, false, nil
			}
		}
	}
	ok, err := ex.nodeMatches(np, other, r)
	if err != nil || !ok {
		return nil, false, err
	}
	r2 := r
	if rp.Var != "" {
		r2 = r2.with(rp.Var, rel)
	}
	if np.Var != "" {
		r2 = r2.with(np.Var, other)
	}
	if rp.Where != nil {
		c, err := ex.eval(rp.Where, r2)
		if err != nil || c != true {
			return nil, false, err
		}
	}
	if np.Where != nil {
		c, err := ex.eval(np.Where, r2)
		if err != nil || c != true {
			return nil, false, err
		}
	}
	return r2, true, nil
}

// walkVarLength matches a variable-length relationship, which binds its
// variable to the list of relationships traversed.
func (ex *exec) walkVarLength(elems []syntax.PatternElem, i int, rp *syntax.RelPattern, np *syntax.NodePattern, cur *Node, r row, ps *pathState, used map[int64]bool, done func(row, *pathState) error) error {
	min := int64(1)
	max := int64(-1) // unbounded
	rng := rangeOf(rp)
	if rng.Min != nil {
		min = *rng.Min
	}
	if rng.Max != nil {
		max = *rng.Max
	}
	{
		hops := ex.g.eng.maxHops()
		if max > hops {
			return argErr("variable-length upper bound %d exceeds the configured maximum of %d hops", max, hops)
		}
		if max < 0 {
			max = hops
		}
	}
	var chain []*Rel
	var visited []*Node // the nodes reached along chain, in order
	var dfs func(node *Node, depth int64) error
	dfs = func(node *Node, depth int64) error {
		if err := ex.tick(); err != nil {
			return err
		}
		if depth >= min {
			r2, ok, err := ex.bindVarLength(rp, np, chain, node, r)
			if err != nil {
				return err
			}
			if ok {
				ps.nodes = append(ps.nodes, visited...)
				ps.rels = append(ps.rels, chain...)
				err := ex.walk(elems, i+2, node, r2, ps, used, done)
				ps.nodes = ps.nodes[:len(ps.nodes)-len(visited)]
				ps.rels = ps.rels[:len(ps.rels)-len(chain)]
				if err != nil {
					return err
				}
			}
		}
		if max >= 0 && depth >= max {
			return nil
		}
		if ex.overBudget(len(ps.rels) + len(chain)) {
			return nil
		}
		return ex.expand(rp, node, r, used, func(rel *Rel, other *Node) error {
			used[rel.ID] = true
			chain = append(chain, rel)
			visited = append(visited, other)
			err := dfs(other, depth+1)
			visited = visited[:len(visited)-1]
			chain = chain[:len(chain)-1]
			delete(used, rel.ID)
			return err
		})
	}
	return dfs(cur, 0)
}

func (ex *exec) bindVarLength(rp *syntax.RelPattern, np *syntax.NodePattern, chain []*Rel, end *Node, r row) (row, bool, error) {
	if np.Var != "" {
		if v, bound := r[np.Var]; bound {
			b, ok := v.(*Node)
			if !ok || b.ID != end.ID {
				return nil, false, nil
			}
		}
	}
	for _, rel := range chain {
		if ok, err := ex.relMatches(rp, rel, r); err != nil || !ok {
			return nil, false, err
		}
	}
	if ok, err := ex.nodeMatches(np, end, r); err != nil || !ok {
		return nil, false, err
	}
	r2 := r
	if rp.Var != "" {
		list := make([]any, len(chain))
		for i, rel := range chain {
			list[i] = rel
		}
		if v, bound := r[rp.Var]; bound {
			if equals(v, list) != triTrue {
				return nil, false, nil
			}
		}
		r2 = r2.with(rp.Var, list)
	}
	if np.Var != "" {
		r2 = r2.with(np.Var, end)
	}
	if np.Where != nil {
		c, err := ex.eval(np.Where, r2)
		if err != nil || c != true {
			return nil, false, err
		}
	}
	return r2, true, nil
}

// ─── shortest paths ──────────────────────────────────────────────────────────

// matchShortest matches shortestPath()/allShortestPaths() and the SHORTEST
// selectors: breadth-first search between two nodes over the pattern's single
// variable-length relationship.
func (ex *exec) matchShortest(part *syntax.PatternPart, r row, used map[int64]bool, cont func(row) error) error {
	if len(part.Elems) != 3 {
		return unsupported("shortest paths over more than one relationship pattern")
	}
	start, ok1 := part.Elems[0].(*syntax.NodePattern)
	rp, ok2 := part.Elems[1].(*syntax.RelPattern)
	end, ok3 := part.Elems[2].(*syntax.NodePattern)
	if !ok1 || !ok2 || !ok3 {
		return unsupported("shortest path pattern")
	}
	all := part.Func == syntax.FuncAllShortestPaths
	if part.Selector != nil && part.Selector.Kind == syntax.SelectorAllShortest {
		all = true
	}
	min, max := int64(1), int64(1) // a plain relationship pattern is one hop
	if rng := rangeOf(rp); rng != nil {
		min, max = 1, -1
		if rng.Min != nil {
			min = *rng.Min
		}
		if rng.Max != nil {
			max = *rng.Max
		}
	}
	return ex.forEachStart(start, nil, r, func(s *Node, r2 row) error {
		return ex.forEachStart(end, nil, r2, func(e *Node, r3 row) error {
			paths, err := ex.shortestPaths(rp, s, e, min, max, all, r3, used)
			if err != nil {
				return err
			}
			for _, p := range paths {
				r4 := r3
				if rp.Var != "" {
					list := make([]any, len(p.Rels))
					for i, rel := range p.Rels {
						list[i] = rel
					}
					r4 = r4.with(rp.Var, list)
				}
				if part.Var != "" {
					r4 = r4.with(part.Var, p)
				}
				if err := cont(r4); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// shortestPaths finds the shortest path(s) from s to e whose length lies in
// [min, max] (max < 0 is unbounded). all asks for every shortest path rather
// than one.
func (ex *exec) shortestPaths(rp *syntax.RelPattern, s, e *Node, min, max int64, all bool, r row, used map[int64]bool) ([]*Path, error) {
	{
		hops := ex.g.eng.maxHops()
		if max > hops {
			return nil, argErr("shortest path upper bound %d exceeds the configured maximum of %d hops", max, hops)
		}
		if max < 0 {
			max = hops
		}
	}
	if s.ID == e.ID || min > 1 {
		return ex.shortestPathsSlow(rp, s, e, min, max, all, r, used)
	}
	if !all && !shortestBidirectionalDisabled {
		return ex.shortestPathBidirectional(rp, s, e, max, r, used)
	}
	return ex.shortestPathsBFS(rp, s, e, max, all, r, used)
}

// maxShortestPaths bounds how many equal-length shortest paths allShortestPaths
// will materialise between one pair of nodes: their number can be exponential.
const maxShortestPaths = 100000

// predecessor is how a node was reached: over rel from node from.
type predecessor struct {
	rel  *Rel
	from *Node
}

// prefetchLevel loads, in a few batched queries, the adjacency of a search
// frontier and then the nodes at the far ends of the relationships that match
// rp, so expanding the level does not issue one query per node. It returns
// those far nodes (without duplicates). When limit is positive and more far
// nodes than that would be loaded, only the adjacency is prefetched and the
// result is nil.
func (ex *exec) prefetchLevel(rp *syntax.RelPattern, frontier []*Node, r row, limit int) ([]*Node, error) {
	if len(frontier) < 2 {
		return nil, nil
	}
	ids := make([]int64, len(frontier))
	for i, n := range frontier {
		ids[i] = n.ID
	}
	var far []int64
	seen := map[int64]bool{}
	for _, outgoing := range []bool{true, false} {
		if (outgoing && rp.Dir == syntax.DirLeft) || (!outgoing && rp.Dir == syntax.DirRight) {
			continue
		}
		if err := ex.g.prefetchAdj(ids, outgoing); err != nil {
			return nil, err
		}
		for _, id := range ids {
			rels, _ := ex.g.loadAdj(id, outgoing)
			for _, rel := range rels {
				// Skip relationships the pattern will reject (wrong type or
				// properties): their far nodes would never be visited. An
				// error here is left for the walk itself to report.
				if ok, err := ex.relMatches(rp, rel, r); err != nil || !ok {
					continue
				}
				id := rel.Start
				if outgoing {
					id = rel.End
				}
				if !seen[id] {
					seen[id] = true
					far = append(far, id)
				}
			}
		}
	}
	if limit > 0 && len(far) > limit {
		return nil, nil
	}
	if err := ex.g.prefetchNodes(far); err != nil {
		return nil, err
	}
	nodes := make([]*Node, 0, len(far))
	for _, id := range far {
		if n, ok := ex.g.nodes[id]; ok && !n.Deleted {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

// prefetchChain prefetches along a path pattern from a set of start nodes:
// for each (relationship, node) pair of rest it loads the adjacency of the
// current frontier and the far nodes, then continues from the far nodes that
// satisfy the node pattern. It stops at the first element it cannot follow
// (a quantified group, a variable-length relationship after its first hop) and
// when the frontier grows past maxPrefetchFrontier.
func (ex *exec) prefetchChain(rest []syntax.PatternElem, frontier []*Node, r row) error {
	for len(rest) >= 2 && len(frontier) > 1 {
		rp, ok := rest[0].(*syntax.RelPattern)
		np, ok2 := rest[1].(*syntax.NodePattern)
		if !ok || !ok2 {
			return nil
		}
		prefetchUsed.Add(1)
		far, err := ex.prefetchLevel(rp, frontier, r, maxPrefetchFrontier)
		if err != nil {
			return err
		}
		if rangeOf(rp) != nil {
			return nil
		}
		next := far[:0:0]
		for _, n := range far {
			ok, err := ex.nodeMatches(np, n, r)
			if err != nil {
				return nil // the walk reports it
			}
			if ok {
				next = append(next, n)
			}
		}
		frontier, rest = next, rest[2:]
	}
	return nil
}

// shortestPathsBFS is a breadth-first search over nodes. Each node is expanded
// once, at the depth it is first reached, and remembers the (rel, node) pairs
// that reach it at that depth, so a path is rebuilt only for the target instead
// of being copied at every step. A shortest path never repeats a node, so the
// relationship-uniqueness rule cannot matter here, except for relationships the
// MATCH has already used (used), which are skipped.
func (ex *exec) shortestPathsBFS(rp *syntax.RelPattern, s, e *Node, max int64, all bool, r row, used map[int64]bool) ([]*Path, error) {
	dist := map[int64]int64{s.ID: 0}
	preds := map[int64][]predecessor{}
	frontier := []*Node{s}
	for depth := int64(0); len(frontier) > 0 && (max < 0 || depth < max); depth++ {
		var next []*Node
		if _, err := ex.prefetchLevel(rp, frontier, r, 0); err != nil {
			return nil, err
		}
		for _, node := range frontier {
			err := ex.expand(rp, node, r, used, func(rel *Rel, other *Node) error {
				d, seen := dist[other.ID]
				switch {
				case !seen:
					dist[other.ID] = depth + 1
					preds[other.ID] = []predecessor{{rel, node}}
					next = append(next, other)
				case all && d == depth+1:
					preds[other.ID] = append(preds[other.ID], predecessor{rel, node})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if _, found := dist[e.ID]; found {
			break // every predecessor at this depth has been recorded
		}
		frontier = next
	}
	if _, found := dist[e.ID]; !found {
		return nil, nil
	}
	return ex.pathsFromPreds(s, e, preds, all)
}

// pathsFromPreds rebuilds the shortest paths from s to e out of the
// predecessor lists: the first one only, or all of them.
func (ex *exec) pathsFromPreds(s, e *Node, preds map[int64][]predecessor, all bool) ([]*Path, error) {
	var out []*Path
	var rels []*Rel
	var nodes []*Node
	var walk func(n *Node) error
	walk = func(n *Node) error {
		if n.ID == s.ID {
			p := &Path{Nodes: []*Node{s}}
			for i := len(nodes) - 1; i >= 0; i-- {
				p.Nodes = append(p.Nodes, nodes[i])
			}
			for i := len(rels) - 1; i >= 0; i-- {
				p.Rels = append(p.Rels, rels[i])
			}
			out = append(out, p)
			if len(out) > maxShortestPaths {
				return unsupported("allShortestPaths found more than %d shortest paths between one pair of nodes", maxShortestPaths)
			}
			return nil
		}
		for _, pr := range preds[n.ID] {
			rels = append(rels, pr.rel)
			nodes = append(nodes, n)
			err := walk(pr.from)
			rels, nodes = rels[:len(rels)-1], nodes[:len(nodes)-1]
			if err != nil || !all {
				return err
			}
		}
		return nil
	}
	if err := walk(e); err != nil {
		return nil, err
	}
	return out, nil
}

// shortestBidirectionalDisabled turns the bidirectional search off (tests
// compare it with the one-directional search).
var shortestBidirectionalDisabled bool

// flipDir returns a copy of rp that traverses in the opposite direction, for
// the backward half of a bidirectional search.
func flipDir(rp *syntax.RelPattern) *syntax.RelPattern {
	c := *rp
	switch rp.Dir {
	case syntax.DirRight:
		c.Dir = syntax.DirLeft
	case syntax.DirLeft:
		c.Dir = syntax.DirRight
	}
	return &c
}

// shortestPathBidirectional finds one shortest path between two nodes by
// searching from both ends, always expanding the smaller frontier, one whole
// level at a time. On a graph that fans out this visits the nodes within about
// half the distance of each end instead of everything within the full
// distance of one.
func (ex *exec) shortestPathBidirectional(rp *syntax.RelPattern, s, e *Node, max int64, r row, used map[int64]bool) ([]*Path, error) {
	back := flipDir(rp)
	distF, distB := map[int64]int64{s.ID: 0}, map[int64]int64{e.ID: 0}
	predF, predB := map[int64]predecessor{}, map[int64]predecessor{}
	frontF, frontB := []*Node{s}, []*Node{e}
	var depthF, depthB int64

	var meet *Node
	best := int64(-1)
	for len(frontF) > 0 && len(frontB) > 0 && meet == nil {
		if max >= 0 && depthF+depthB >= max {
			return nil, nil
		}
		forward := len(frontF) <= len(frontB)
		pattern, dist, other, pred, front, depth := rp, distF, distB, predF, &frontF, &depthF
		if !forward {
			pattern, dist, other, pred, front, depth = back, distB, distF, predB, &frontB, &depthB
		}
		var next []*Node
		if _, err := ex.prefetchLevel(pattern, *front, r, 0); err != nil {
			return nil, err
		}
		for _, node := range *front {
			err := ex.expand(pattern, node, r, used, func(rel *Rel, n *Node) error {
				if _, seen := dist[n.ID]; seen {
					return nil
				}
				dist[n.ID] = *depth + 1
				pred[n.ID] = predecessor{rel, node}
				next = append(next, n)
				if od, ok := other[n.ID]; ok {
					if total := *depth + 1 + od; best < 0 || total < best {
						best, meet = total, n
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		*front = next
		*depth++
	}
	if meet == nil || (max >= 0 && best > max) {
		return nil, nil
	}
	// s ... meet from the forward predecessors, meet ... e from the backward ones.
	p := &Path{Nodes: []*Node{meet}}
	for n := meet; n.ID != s.ID; {
		pr := predF[n.ID]
		p.Nodes = append([]*Node{pr.from}, p.Nodes...)
		p.Rels = append([]*Rel{pr.rel}, p.Rels...)
		n = pr.from
	}
	for n := meet; n.ID != e.ID; {
		pr := predB[n.ID]
		p.Nodes = append(p.Nodes, pr.from)
		p.Rels = append(p.Rels, pr.rel)
		n = pr.from
	}
	return []*Path{p}, nil
}

// shortestPathsSlow finds the shortest path(s) from s to e by extending whole
// paths level by level, which keeps relationship uniqueness exact but holds one
// state per path (exponential on graphs with many equal-length routes). It is
// the fallback for the cases the node-based search cannot answer: a path that
// must return to its start, and a minimum length above one.
func (ex *exec) shortestPathsSlow(rp *syntax.RelPattern, s, e *Node, min, max int64, all bool, r row, used map[int64]bool) ([]*Path, error) {
	type state struct {
		node *Node
		path *Path
	}
	if s.ID == e.ID && min == 0 {
		return []*Path{{Nodes: []*Node{s}}}, nil
	}
	var found []*Path
	frontier := []state{{s, &Path{Nodes: []*Node{s}}}}
	for depth := int64(0); len(frontier) > 0 && (max < 0 || depth < max) && len(found) == 0; depth++ {
		var next []state
		for _, st := range frontier {
			taken := map[int64]bool{}
			for _, rel := range st.path.Rels {
				taken[rel.ID] = true
			}
			err := ex.expand(rp, st.node, r, mergeUsed(used, taken), func(rel *Rel, other *Node) error {
				p := &Path{
					Nodes: append(append([]*Node{}, st.path.Nodes...), other),
					Rels:  append(append([]*Rel{}, st.path.Rels...), rel),
				}
				if other.ID == e.ID && int64(len(p.Rels)) >= min {
					found = append(found, p)
					return nil
				}
				next = append(next, state{other, p})
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if len(found) > 0 {
			break
		}
		frontier = next
		if len(frontier) > 100000 {
			return nil, unsupported("shortest path search space is too large")
		}
	}
	if !all && len(found) > 1 {
		found = found[:1]
	}
	return found, nil
}

func mergeUsed(a, b map[int64]bool) map[int64]bool {
	out := make(map[int64]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// ─── pattern expressions ─────────────────────────────────────────────────────

// patternExpr evaluates a pattern predicate (true if the pattern matches from
// the row's bound variables) or a shortestPath() call (the path, or null).
func (ex *exec) patternExpr(e *syntax.PatternExpr, r row) (any, error) {
	if e.Part.Func != syntax.FuncNone {
		var result any
		err := ex.matchParts([]*syntax.PatternPart{e.Part}, r, func(r2 row) error {
			// The path is the part's last relationship-less value: rebuild it.
			if e.Part.Var != "" {
				result = r2[e.Part.Var]
			} else {
				p := ex.partPath(e.Part, r2)
				result = p
			}
			return errStop
		})
		if err != nil && err != errStop {
			return nil, err
		}
		return result, nil
	}
	found := false
	err := ex.matchParts([]*syntax.PatternPart{e.Part}, r, func(row) error {
		found = true
		return errStop
	})
	if err != nil && err != errStop {
		return nil, err
	}
	return found, nil
}

// partPath assembles the path of a matched part from its bound variables.
func (ex *exec) partPath(part *syntax.PatternPart, r row) *Path {
	p := &Path{}
	for _, el := range part.Elems {
		switch el := el.(type) {
		case *syntax.NodePattern:
			if n, ok := r[el.Var].(*Node); ok {
				p.Nodes = append(p.Nodes, n)
			}
		case *syntax.RelPattern:
			switch v := r[el.Var].(type) {
			case *Rel:
				p.Rels = append(p.Rels, v)
			case []any:
				for _, x := range v {
					if rel, ok := x.(*Rel); ok {
						p.Rels = append(p.Rels, rel)
					}
				}
			}
		}
	}
	return p
}

// errStop ends a match enumeration early without being an error.
var errStop = errorf("internal", "stop", "stop")

func (ex *exec) patternComp(e *syntax.PatternComp, r row) (any, error) {
	out := []any{}
	err := ex.matchParts([]*syntax.PatternPart{e.Pattern}, r, func(r2 row) error {
		if e.Where != nil {
			c, err := ex.eval(e.Where, r2)
			if err != nil {
				return err
			}
			if c != true {
				return nil
			}
		}
		v, err := ex.eval(e.Proj, r2)
		if err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// subquery evaluates EXISTS { }, COUNT { } and COLLECT { }.
func (ex *exec) subquery(e *syntax.SubqueryExpr, r row) (any, error) {
	var rows []row
	var cols []string
	switch {
	case e.Query != nil:
		var err error
		cols, rows, err = ex.runBody(e.Query, []row{r})
		if err != nil {
			return nil, err
		}
	default:
		err := ex.matchParts(e.Patterns, r, func(r2 row) error {
			if e.Where != nil {
				c, err := ex.eval(e.Where, r2)
				if err != nil {
					return err
				}
				if c != true {
					return nil
				}
			}
			rows = append(rows, r2)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	switch e.Kind {
	case syntax.SubqueryExists:
		return len(rows) > 0, nil
	case syntax.SubqueryCount:
		return int64(len(rows)), nil
	}
	out := make([]any, 0, len(rows))
	for _, rw := range rows {
		if len(cols) == 0 {
			return nil, unsupported("COLLECT without a column")
		}
		out = append(out, rw[cols[0]])
	}
	return out, nil
}
