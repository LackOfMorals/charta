package interp

import (
	"context"
	"sort"

	"github.com/LackOfMorals/charta/cypher/proc"
	"github.com/LackOfMorals/charta/cypher/syntax"
)

// resolve finds the procedure a CALL names: registered first, then built-in.
func (ex *exec) resolve(name string) (*proc.Procedure, bool) {
	if p, ok := ex.procs.Get(name); ok {
		return p, true
	}
	for _, sig := range proc.Builtin {
		if proc.Key(sig.Name) == proc.Key(name) {
			return &proc.Procedure{Signature: *sig, Fn: ex.builtinProc(sig.Name)}, true
		}
	}
	return nil, false
}

// callColumns is the output column list of a standalone CALL.
func (ex *exec) callColumns(cl *syntax.Call) []string {
	if cl.Yield != nil && !cl.Yield.Star {
		var cols []string
		for _, y := range cl.Yield.Items {
			if y.Alias != "" {
				cols = append(cols, y.Alias)
			} else {
				cols = append(cols, y.Name)
			}
		}
		return cols
	}
	p, ok := ex.resolve(joinName(cl.Name))
	if !ok {
		return nil
	}
	var cols []string
	for _, o := range p.Outputs {
		cols = append(cols, o.Name)
	}
	return cols
}

func joinName(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += "."
		}
		s += p
	}
	return s
}

func (ex *exec) execCall(cl *syntax.Call, st *qstate) error {
	name := joinName(cl.Name)
	p, ok := ex.resolve(name)
	if !ok {
		return errorf("ProcedureError", "ProcedureNotFound", "there is no procedure with the name `%s` registered", name)
	}
	// Output name -> variable name.
	type out struct{ field, variable string }
	var outs []out
	if cl.Yield == nil || cl.Yield.Star {
		for _, o := range p.Outputs {
			outs = append(outs, out{o.Name, o.Name})
		}
	} else {
		for _, y := range cl.Yield.Items {
			v := y.Alias
			if v == "" {
				v = y.Name
			}
			outs = append(outs, out{y.Name, v})
		}
	}
	for _, o := range outs {
		st.declare(o.variable)
	}

	var result []row
	for _, r := range st.rows {
		args, err := ex.callArgs(cl, p, r)
		if err != nil {
			return err
		}
		recs, err := p.Fn(ex.g.ctx, args)
		if err != nil {
			return err
		}
		if len(p.Outputs) == 0 {
			// A procedure without outputs runs for its effect; rows pass through.
			result = append(result, r)
			continue
		}
		matched := false
		for _, rec := range recs {
			nr := r
			for _, o := range outs {
				nr = nr.with(o.variable, rec[o.field])
			}
			if cl.Yield != nil && cl.Yield.Where != nil {
				c, err := ex.eval(cl.Yield.Where, nr)
				if err != nil {
					return err
				}
				if c != true {
					continue
				}
			}
			matched = true
			result = append(result, nr)
		}
		if !matched && cl.Optional {
			nr := r
			for _, o := range outs {
				nr = nr.with(o.variable, nil)
			}
			result = append(result, nr)
		}
	}
	st.rows = result
	return nil
}

// callArgs evaluates and coerces the arguments of a call for one row.
func (ex *exec) callArgs(cl *syntax.Call, p *proc.Procedure, r row) ([]any, error) {
	var raw []any
	if cl.ArgsOmitted {
		for _, in := range p.Inputs {
			v, ok := ex.params[in.Name]
			if !ok {
				return nil, errorf("ParameterMissing", "MissingParameter", "expected parameter `%s`", in.Name)
			}
			raw = append(raw, v)
		}
	} else {
		for _, a := range cl.Args {
			v, err := ex.eval(a, r)
			if err != nil {
				return nil, err
			}
			raw = append(raw, v)
		}
	}
	if len(raw) != len(p.Inputs) {
		return nil, errorf("SyntaxError", "InvalidNumberOfArguments", "procedure `%s` expects %d arguments but got %d", p.Name, len(p.Inputs), len(raw))
	}
	args := make([]any, len(raw))
	for i, v := range raw {
		c, err := proc.Coerce(p.Inputs[i], v)
		if err != nil {
			return nil, errorf("SyntaxError", "InvalidArgumentType", "%v", err)
		}
		args[i] = c
	}
	return args, nil
}

// builtinProc implements the db.* procedures over the store.
func (ex *exec) builtinProc(name string) proc.Func {
	var q, col string
	switch proc.Key(name) {
	case "db.index.vector.querynodes":
		return ex.queryVectorNodes
	case "db.labels":
		q, col = `SELECT DISTINCT label FROM node_labels ORDER BY label`, "label"
	case "db.relationshiptypes":
		q, col = `SELECT DISTINCT type FROM edges ORDER BY type`, "relationshipType"
	default:
		q, col = `SELECT DISTINCT j.key FROM (SELECT props FROM nodes UNION ALL SELECT props FROM edges), json_each(props) AS j ORDER BY j.key`, "propertyKey"
	}
	return func(ctx context.Context, _ []any) ([]map[string]any, error) {
		rows, err := ex.g.db.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var vals []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return nil, err
			}
			vals = append(vals, s)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		sort.Strings(vals)
		out := make([]map[string]any, len(vals))
		for i, v := range vals {
			out[i] = map[string]any{col: v}
		}
		return out, nil
	}
}
