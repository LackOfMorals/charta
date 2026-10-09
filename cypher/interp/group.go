package interp

import (
	"sort"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// groupVars lists the variables a group pattern declares, in order.
func groupVars(g *syntax.GroupPattern) []string {
	var out []string
	for _, el := range g.Elems {
		switch el := el.(type) {
		case *syntax.NodePattern:
			if el.Var != "" {
				out = append(out, el.Var)
			}
		case *syntax.RelPattern:
			if el.Var != "" {
				out = append(out, el.Var)
			}
		}
	}
	return out
}

// walkGroup matches the group pattern elems[i] starting at cur, once per
// iteration of its quantifier, then carries on with the rest of the pattern.
//
// Variables declared inside a quantified group become lists with one entry per
// iteration (group variables). The node a group ends on is the node the next
// element is matched against, and the node it starts on is the node before it:
// juxtaposed node patterns describe one and the same node.
func (ex *exec) walkGroup(elems []syntax.PatternElem, i int, g *syntax.GroupPattern, cur *Node, r row, ps *pathState, used map[int64]bool, done func(row, *pathState) error) error {
	if len(g.Elems) == 0 {
		return unsupported("an empty path pattern group")
	}
	for _, el := range g.Elems {
		if _, nested := el.(*syntax.GroupPattern); nested {
			return unsupported("nested quantified path patterns")
		}
	}
	first, ok := g.Elems[0].(*syntax.NodePattern)
	if !ok {
		return unsupported("a path pattern group that does not start with a node pattern")
	}
	vars := groupVars(g)
	hasRel := false
	for _, el := range g.Elems {
		if _, isRel := el.(*syntax.RelPattern); isRel {
			hasRel = true
		}
	}

	// after continues with the rest of the pattern from the node the group ended on.
	after := func(end *Node, r2 row) error {
		j := i + 1
		if j >= len(elems) {
			return done(r2, ps)
		}
		switch el := elems[j].(type) {
		case *syntax.NodePattern:
			if el.Var != "" {
				if v, bound := r2[el.Var]; bound {
					b, isNode := v.(*Node)
					if !isNode || b.ID != end.ID {
						return nil
					}
				}
			}
			ok, err := ex.nodeMatches(el, end, r2)
			if err != nil || !ok {
				return err
			}
			if el.Var != "" {
				r2 = r2.with(el.Var, end)
			}
			if el.Where != nil {
				c, err := ex.eval(el.Where, r2)
				if err != nil || c != true {
					return err
				}
			}
			return ex.walk(elems, j+1, end, r2, ps, used, done)
		default:
			return ex.walk(elems, j, end, r2, ps, used, done)
		}
	}

	// Unquantified group: one iteration, variables stay singletons.
	if g.Quant == nil {
		return ex.groupIteration(g, first, cur, r, ps, used, func(end *Node, r2 row) error {
			return after(end, r2)
		})
	}
	if !hasRel {
		return unsupported("a quantified path pattern without a relationship")
	}

	min, max := g.Quant.Min, int64(-1)
	if g.Quant.Max != nil {
		max = *g.Quant.Max
	}
	if ex.g.eng != nil && ex.g.eng.MaxPathHops > 0 {
		hops := int64(ex.g.eng.MaxPathHops)
		if max > hops {
			return argErr("quantifier upper bound %d exceeds the configured maximum of %d hops", max, hops)
		}
		if max < 0 {
			max = hops
		}
	}

	lists := map[string][]any{}
	var iterate func(n int64, node *Node) error
	iterate = func(n int64, node *Node) error {
		if n >= min {
			r2 := r
			for _, v := range vars {
				r2 = r2.with(v, append([]any{}, lists[v]...))
			}
			if err := after(node, r2); err != nil {
				return err
			}
		}
		if max >= 0 && n >= max {
			return nil
		}
		return ex.groupIteration(g, first, node, r, ps, used, func(end *Node, local row) error {
			for _, v := range vars {
				lists[v] = append(lists[v], local[v])
			}
			err := iterate(n+1, end)
			for _, v := range vars {
				lists[v] = lists[v][:len(lists[v])-1]
			}
			return err
		})
	}
	return iterate(0, cur)
}

// groupIteration matches the group's inner pattern once, starting at node, and
// calls fn with the node it ended on and the iteration's own bindings.
func (ex *exec) groupIteration(g *syntax.GroupPattern, first *syntax.NodePattern, node *Node, r row, ps *pathState, used map[int64]bool, fn func(end *Node, local row) error) error {
	local := r
	if first.Var != "" {
		if v, bound := local[first.Var]; bound {
			// A variable bound outside must be the same node (only meaningful
			// for non-quantified groups; group variables are fresh per iteration).
			if g.Quant == nil {
				if b, isNode := v.(*Node); !isNode || b.ID != node.ID {
					return nil
				}
			}
		}
	}
	ok, err := ex.nodeMatches(first, node, local)
	if err != nil || !ok {
		return err
	}
	if first.Var != "" {
		local = local.with(first.Var, node)
	}
	if first.Where != nil {
		c, err := ex.eval(first.Where, local)
		if err != nil || c != true {
			return err
		}
	}
	return ex.walk(g.Elems, 1, node, local, ps, used, func(r2 row, ps2 *pathState) error {
		if g.Where != nil {
			c, err := ex.eval(g.Where, r2)
			if err != nil || c != true {
				return err
			}
		}
		return fn(ps2.nodes[len(ps2.nodes)-1], r2)
	})
}

// bfsSelectable reports whether a shortest-path part can be answered by the
// breadth-first search in matchShortest: a single relationship pattern between
// two node patterns under shortestPath()/allShortestPaths()/ANY SHORTEST/ALL
// SHORTEST.
func (ex *exec) bfsSelectable(part *syntax.PatternPart) bool {
	if len(part.Elems) != 3 {
		return false
	}
	_, ok1 := part.Elems[0].(*syntax.NodePattern)
	_, ok2 := part.Elems[1].(*syntax.RelPattern)
	_, ok3 := part.Elems[2].(*syntax.NodePattern)
	if !ok1 || !ok2 || !ok3 {
		return false
	}
	if part.Selector == nil {
		return part.Func != syntax.FuncNone
	}
	k := part.Selector.Kind
	return k == syntax.SelectorAnyShortest || k == syntax.SelectorAllShortest
}

// matchSelected applies a path selector to the paths a pattern matches: the
// matches are partitioned by their (start, end) nodes and each partition is
// reduced as the selector says.
func (ex *exec) matchSelected(part *syntax.PatternPart, r row, used map[int64]bool, cont func(row) error) error {
	if part.Func != syntax.FuncNone {
		return unsupported("shortestPath() over a pattern with more than one relationship pattern")
	}
	sel := part.Selector
	k := int64(1)
	if sel.K != nil {
		v, err := ex.eval(sel.K, r)
		if err != nil {
			return err
		}
		n, ok := v.(int64)
		if !ok || n < 1 {
			return argErr("a path selector count must be a positive integer, got %v", v)
		}
		k = n
	}
	inner := *part
	inner.Selector = nil
	pathVar := part.Var
	if pathVar == "" {
		pathVar = "\x00path"
		inner.Var = pathVar
	}
	type match struct {
		row    row
		length int
	}
	groups := map[[2]int64][]match{}
	var order [][2]int64
	err := ex.matchPart(&inner, r, used, func(r2 row) error {
		p := r2[pathVar].(*Path)
		key := [2]int64{p.Nodes[0].ID, p.Nodes[len(p.Nodes)-1].ID}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], match{r2, len(p.Rels)})
		return nil
	})
	if err != nil {
		return err
	}
	for _, key := range order {
		ms := groups[key]
		sort.SliceStable(ms, func(a, b int) bool { return ms[a].length < ms[b].length })
		var chosen []match
		switch sel.Kind {
		case syntax.SelectorAll:
			chosen = ms
		case syntax.SelectorAny:
			chosen = ms[:min64(k, int64(len(ms)))]
		case syntax.SelectorAnyShortest:
			chosen = ms[:1]
		case syntax.SelectorAllShortest:
			for _, m := range ms {
				if m.length != ms[0].length {
					break
				}
				chosen = append(chosen, m)
			}
		case syntax.SelectorShortest:
			chosen = ms[:min64(k, int64(len(ms)))]
		case syntax.SelectorShortestGroups:
			distinct := int64(0)
			last := -1
			for _, m := range ms {
				if m.length != last {
					distinct++
					last = m.length
					if distinct > k {
						break
					}
				}
				chosen = append(chosen, m)
			}
		}
		for _, m := range chosen {
			out := m.row
			if part.Var == "" {
				out = out.without(pathVar)
			}
			if err := cont(out); err != nil {
				return err
			}
		}
	}
	return nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
