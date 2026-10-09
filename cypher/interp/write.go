package interp

import (
	"github.com/LackOfMorals/charta/cypher/syntax"
)

// labelNames flattens a conjunction of labels (`:A:B`, `A&B`, `$(expr)`) into
// names, as CREATE and SET need.
func (ex *exec) labelNames(le syntax.LabelExpr, r row) ([]string, error) {
	switch le := le.(type) {
	case nil:
		return nil, nil
	case *syntax.LabelName:
		if le.Dynamic == nil {
			return []string{le.Name}, nil
		}
		v, err := ex.eval(le.Dynamic, r)
		if err != nil {
			return nil, err
		}
		switch x := v.(type) {
		case string:
			return []string{x}, nil
		case []any:
			out := make([]string, 0, len(x))
			for _, el := range x {
				s, ok := el.(string)
				if !ok {
					return nil, typeErr("dynamic labels must be strings, got %s", typeName(el))
				}
				out = append(out, s)
			}
			return out, nil
		}
		return nil, typeErr("dynamic labels must be strings, got %s", typeName(v))
	case *syntax.LabelAnd:
		l, err := ex.labelNames(le.L, r)
		if err != nil {
			return nil, err
		}
		rr, err := ex.labelNames(le.R, r)
		if err != nil {
			return nil, err
		}
		return append(l, rr...), nil
	}
	return nil, unsupported("label expression in an update")
}

// propMap evaluates a property specification (map literal or parameter).
func (ex *exec) propMap(spec syntax.Expr, r row) (map[string]any, error) {
	switch p := spec.(type) {
	case nil:
		return map[string]any{}, nil
	case *syntax.MapLit:
		out := make(map[string]any, len(p.Entries))
		for _, en := range p.Entries {
			v, err := ex.eval(en.Value, r)
			if err != nil {
				return nil, err
			}
			out[en.Key] = v
		}
		return out, nil
	case *syntax.Param:
		v, err := ex.eval(p, r)
		if err != nil {
			return nil, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, typeErr("a property map parameter must be a map, got %s", typeName(v))
		}
		return m, nil
	}
	return nil, unsupported("property specification %T", spec)
}

// ─── CREATE ──────────────────────────────────────────────────────────────────

func (ex *exec) execCreate(cl *syntax.Create, st *qstate) error {
	st.declare(patternVars(cl.Patterns)...)
	out := make([]row, 0, len(st.rows))
	for _, r := range st.rows {
		var err error
		for _, part := range cl.Patterns {
			if r, err = ex.createPart(part, r); err != nil {
				return err
			}
		}
		out = append(out, r)
	}
	st.rows = out
	return nil
}

// createNodeFor returns the node a pattern refers to: the bound one, or a new one.
func (ex *exec) createNodeFor(np *syntax.NodePattern, r row) (*Node, row, error) {
	if np.Var != "" {
		if v, bound := r[np.Var]; bound {
			n, ok := v.(*Node)
			if !ok {
				return nil, nil, typeErr("`%s` is not a node", np.Var)
			}
			return n, r, nil
		}
	}
	labels, err := ex.labelNames(np.Labels, r)
	if err != nil {
		return nil, nil, err
	}
	props, err := ex.propMap(np.Props, r)
	if err != nil {
		return nil, nil, err
	}
	n, err := ex.g.createNode(labels, props)
	if err != nil {
		return nil, nil, err
	}
	if np.Var != "" {
		r = r.with(np.Var, n)
	}
	return n, r, nil
}

func (ex *exec) createPart(part *syntax.PatternPart, r row) (row, error) {
	for _, el := range part.Elems {
		if _, ok := el.(*syntax.GroupPattern); ok {
			return nil, unsupported("quantified patterns in CREATE")
		}
	}
	first := part.Elems[0].(*syntax.NodePattern)
	prev, r, err := ex.createNodeFor(first, r)
	if err != nil {
		return nil, err
	}
	path := &Path{Nodes: []*Node{prev}}
	for i := 1; i+1 < len(part.Elems); i += 2 {
		rp := part.Elems[i].(*syntax.RelPattern)
		np := part.Elems[i+1].(*syntax.NodePattern)
		next, r2, err := ex.createNodeFor(np, r)
		if err != nil {
			return nil, err
		}
		r = r2
		typ, err := ex.relType(rp, r)
		if err != nil {
			return nil, err
		}
		props, err := ex.propMap(rp.Props, r)
		if err != nil {
			return nil, err
		}
		start, end := prev, next
		if rp.Dir == syntax.DirLeft {
			start, end = next, prev
		}
		rel, err := ex.g.createRel(typ, start, end, props)
		if err != nil {
			return nil, err
		}
		if rp.Var != "" {
			r = r.with(rp.Var, rel)
		}
		path.Nodes = append(path.Nodes, next)
		path.Rels = append(path.Rels, rel)
		prev = next
	}
	if part.Var != "" {
		r = r.with(part.Var, path)
	}
	return r, nil
}

func (ex *exec) relType(rp *syntax.RelPattern, r row) (string, error) {
	ln, ok := rp.Types.(*syntax.LabelName)
	if !ok {
		return "", errorf("SyntaxError", "NoSingleRelationshipType", "exactly one relationship type must be specified")
	}
	if ln.Dynamic == nil {
		return ln.Name, nil
	}
	v, err := ex.eval(ln.Dynamic, r)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", typeErr("a dynamic relationship type must be a string, got %s", typeName(v))
	}
	return s, nil
}

// ─── MERGE ───────────────────────────────────────────────────────────────────

func (ex *exec) execMerge(cl *syntax.Merge, st *qstate) error {
	st.declare(patternVars([]*syntax.PatternPart{cl.Pattern})...)
	var out []row
	for _, r := range st.rows {
		if err := ex.checkMergeProps(cl.Pattern, r); err != nil {
			return err
		}
		var matches []row
		err := ex.matchParts([]*syntax.PatternPart{cl.Pattern}, r, func(r2 row) error {
			matches = append(matches, r2)
			return nil
		})
		if err != nil {
			return err
		}
		if len(matches) > 0 {
			for _, m := range matches {
				for _, a := range cl.Actions {
					if !a.OnCreate {
						if err := ex.applySet(a.Items, m); err != nil {
							return err
						}
					}
				}
				out = append(out, m)
			}
			continue
		}
		created, err := ex.createPart(cl.Pattern, r)
		if err != nil {
			return err
		}
		for _, a := range cl.Actions {
			if a.OnCreate {
				if err := ex.applySet(a.Items, created); err != nil {
					return err
				}
			}
		}
		out = append(out, created)
	}
	st.rows = out
	return nil
}

// checkMergeProps rejects null property values in a MERGE pattern.
func (ex *exec) checkMergeProps(part *syntax.PatternPart, r row) error {
	for _, el := range part.Elems {
		var spec syntax.Expr
		switch el := el.(type) {
		case *syntax.NodePattern:
			spec = el.Props
		case *syntax.RelPattern:
			spec = el.Props
		}
		props, err := ex.propMap(spec, r)
		if err != nil {
			return err
		}
		for k, v := range props {
			if v == nil {
				return errorf("SemanticError", "MergeReadOwnWrites", "cannot merge with a null property value (%s)", k)
			}
		}
	}
	return nil
}

// ─── SET / REMOVE / DELETE / FOREACH ─────────────────────────────────────────

func (ex *exec) applySet(items []syntax.SetItem, r row) error {
	for _, it := range items {
		switch it.Kind {
		case syntax.SetProperty:
			value, err := ex.eval(it.Value, r)
			if err != nil {
				return err
			}
			if err := ex.setTarget(it.Target, value, r); err != nil {
				return err
			}
		case syntax.SetReplace, syntax.SetMerge:
			target, err := ex.eval(it.Target, r)
			if err != nil {
				return err
			}
			if target == nil {
				continue
			}
			value, err := ex.eval(it.Value, r)
			if err != nil {
				return err
			}
			props, err := propertiesOf(value)
			if err != nil {
				return err
			}
			if it.Kind == syntax.SetReplace {
				for _, k := range existingKeys(target) {
					if _, keep := props[k]; !keep {
						if err := ex.setEntityProp(target, k, nil); err != nil {
							return err
						}
					}
				}
			}
			for k, v := range props {
				if err := ex.setEntityProp(target, k, v); err != nil {
					return err
				}
			}
		case syntax.SetLabels:
			target, err := ex.eval(it.Target, r)
			if err != nil {
				return err
			}
			if target == nil {
				continue
			}
			n, ok := target.(*Node)
			if !ok {
				return typeErr("cannot set labels on %s", typeName(target))
			}
			labels, err := ex.labelNames(it.Labels, r)
			if err != nil {
				return err
			}
			if err := ex.g.addLabels(n, labels); err != nil {
				return err
			}
		}
	}
	return nil
}

func existingKeys(target any) []string {
	switch x := target.(type) {
	case *Node:
		return sortedKeys(x.Props)
	case *Rel:
		return sortedKeys(x.Props)
	}
	return nil
}

// propertiesOf converts a SET right-hand side (map, node or relationship) to a
// property map.
func propertiesOf(v any) (map[string]any, error) {
	switch x := v.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return x, nil
	case *Node:
		return copyMap(x.Props), nil
	case *Rel:
		return copyMap(x.Props), nil
	}
	return nil, typeErr("expected a map, got %s", typeName(v))
}

// setTarget assigns value to a property target (`n.key` or `n[expr]`).
func (ex *exec) setTarget(target syntax.Expr, value any, r row) error {
	switch t := target.(type) {
	case *syntax.Property:
		subject, err := ex.eval(t.Subject, r)
		if err != nil {
			return err
		}
		return ex.setEntityProp(subject, t.Key, value)
	case *syntax.Subscript:
		subject, err := ex.eval(t.Subject, r)
		if err != nil {
			return err
		}
		k, err := ex.eval(t.Index, r)
		if err != nil {
			return err
		}
		key, ok := k.(string)
		if !ok {
			return errorf("TypeError", "MapElementAccessByNonString", "property keys must be strings, got %s", typeName(k))
		}
		return ex.setEntityProp(subject, key, value)
	}
	return unsupported("SET target %T", target)
}

func (ex *exec) setEntityProp(subject any, key string, value any) error {
	switch x := subject.(type) {
	case nil:
		return nil
	case *Node:
		return ex.g.setNodeProp(x, key, value)
	case *Rel:
		return ex.g.setRelProp(x, key, value)
	}
	return typeErr("cannot set a property on %s", typeName(subject))
}

func (ex *exec) applyRemove(cl *syntax.Remove, r row) error {
	for _, it := range cl.Items {
		if it.Labels != nil {
			target, err := ex.eval(it.Target, r)
			if err != nil {
				return err
			}
			if target == nil {
				continue
			}
			n, ok := target.(*Node)
			if !ok {
				return typeErr("cannot remove labels from %s", typeName(target))
			}
			labels, err := ex.labelNames(it.Labels, r)
			if err != nil {
				return err
			}
			if err := ex.g.removeLabels(n, labels); err != nil {
				return err
			}
			continue
		}
		if err := ex.setTarget(it.Target, nil, r); err != nil {
			return err
		}
	}
	return nil
}

func (ex *exec) applyDelete(cl *syntax.Delete, r row) error {
	for _, e := range cl.Exprs {
		v, err := ex.eval(e, r)
		if err != nil {
			return err
		}
		if err := ex.deleteValue(v, cl.Detach); err != nil {
			return err
		}
	}
	return nil
}

func (ex *exec) deleteValue(v any, detach bool) error {
	switch x := v.(type) {
	case nil:
		return nil
	case *Node:
		return ex.g.deleteNode(x, detach)
	case *Rel:
		ex.g.deleteRel(x)
		return nil
	case *Path:
		for _, rel := range x.Rels {
			ex.g.deleteRel(rel)
		}
		for _, n := range x.Nodes {
			if err := ex.g.deleteNode(n, detach); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, el := range x {
			if err := ex.deleteValue(el, detach); err != nil {
				return err
			}
		}
		return nil
	}
	return errorf("TypeError", "InvalidArgumentType", "cannot delete %s", typeName(v))
}

func (ex *exec) applyForeach(cl *syntax.Foreach, r row) error {
	list, ok, err := ex.list(cl.In, r, "FOREACH")
	if err != nil || !ok {
		return err
	}
	for _, el := range list {
		st := &qstate{rows: []row{r.with(cl.Var, el)}}
		for _, c := range cl.Body {
			if _, err := ex.clause(c, st, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
