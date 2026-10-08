package cypher

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// lowerExpr converts a syntax expression to the Query AST's Expr. It mirrors
// the decisions of the original ANTLR-based builder: forms the Query AST models
// are typed; the rest (multiplicative/power/unary arithmetic, subscripts,
// nested property access, function calls other than the aggregates and
// exists(), comprehensions, pattern predicates, map literals, …) become a
// RawExpr carrying a rendering of the expression, which the translator
// rejects unless it is a bare identifier.
func lowerExpr(e syntax.Expr) (Expr, error) {
	switch e := e.(type) {
	case *syntax.Binary:
		return lowerBinary(e)
	case *syntax.Unary:
		if e.Op == syntax.OpNot {
			n, inner := 0, syntax.Expr(e)
			for {
				u, ok := inner.(*syntax.Unary)
				if !ok || u.Op != syntax.OpNot {
					break
				}
				n++
				inner = u.X
			}
			x, err := lowerExpr(inner)
			if err != nil {
				return nil, err
			}
			if n%2 == 1 {
				return &NotExpr{Expr: x}, nil
			}
			return x, nil
		}
		return raw(e), nil
	case *syntax.Comparison:
		var result Expr
		for i, op := range e.Ops {
			l, err := lowerExpr(e.Operands[i])
			if err != nil {
				return nil, err
			}
			r, err := lowerExpr(e.Operands[i+1])
			if err != nil {
				return nil, err
			}
			cmp := &ComparisonExpr{Left: l, Op: string(op), Right: r}
			if result == nil {
				result = cmp
			} else {
				result = &BoolExpr{Left: result, Op: "AND", Right: cmp}
			}
		}
		return result, nil
	case *syntax.Ident:
		return &VarExpr{Name: e.Name}, nil
	case *syntax.Param:
		return &ParamRef{Name: e.Name}, nil
	case *syntax.Property:
		if id, ok := e.Subject.(*syntax.Ident); ok {
			return &PropExpr{Variable: id.Name, Property: e.Key}, nil
		}
		return raw(e), nil
	case *syntax.HasLabels:
		id, ok := e.X.(*syntax.Ident)
		if !ok {
			return raw(e), nil
		}
		labels, err := labelList(e.Labels)
		if err != nil {
			return nil, err
		}
		return &HasLabelExpr{Variable: id.Name, Labels: labels}, nil
	case *syntax.IsNull:
		if isPredicateChain(e.X) {
			return raw(e), nil
		}
		x, err := lowerExpr(e.X)
		if err != nil {
			return nil, err
		}
		return &NullCheckExpr{Expr: x, IsNotNull: e.Negated}, nil
	case *syntax.IntLit:
		if strings.HasPrefix(strings.TrimSpace(e.Text), "-") {
			return raw(e), nil // a negated literal was a unary expression in the legacy AST
		}
		v, err := strconv.ParseInt(e.Text, 0, 64)
		if err != nil {
			return &RawExpr{Text: e.Text}, nil
		}
		return &LiteralExpr{Value: v}, nil
	case *syntax.FloatLit:
		if strings.HasPrefix(strings.TrimSpace(e.Text), "-") {
			return raw(e), nil
		}
		return &LiteralExpr{Value: e.Value}, nil
	case *syntax.StringLit:
		return &LiteralExpr{Value: e.Value}, nil
	case *syntax.BoolLit:
		return &LiteralExpr{Value: e.Value}, nil
	case *syntax.NullLit:
		return &LiteralExpr{Value: nil}, nil
	case *syntax.ListLit:
		items := make([]Expr, 0, len(e.Elems))
		for _, el := range e.Elems {
			it, err := lowerExpr(el)
			if err != nil {
				return nil, err
			}
			items = append(items, it)
		}
		return &ListLiteralExpr{Items: items}, nil
	case *syntax.FuncCall:
		return lowerFuncCall(e)
	case *syntax.Case:
		return lowerCase(e)
	case *syntax.MapLit, *syntax.Subscript, *syntax.Slice, *syntax.ListComp, *syntax.PatternComp,
		*syntax.Quantifier, *syntax.PatternExpr:
		return raw(e), nil
	}
	return nil, unsupported("%s", exprKindName(e))
}

// exprKindName names an expression form for "not supported" messages.
func exprKindName(e syntax.Expr) string {
	switch e.(type) {
	case *syntax.SubqueryExpr:
		return "an EXISTS/COUNT/COLLECT subquery"
	case *syntax.TypePredicate:
		return "a type predicate (IS :: type)"
	case *syntax.Normalized:
		return "IS NORMALIZED"
	case *syntax.MapProjection:
		return "a map projection"
	case *syntax.Reduce:
		return "reduce()"
	}
	return fmt.Sprintf("this expression (%T)", e)
}

func raw(e syntax.Expr) Expr { return &RawExpr{Text: exprString(e)} }

// isPredicateChain reports whether e is itself an IN / string / IS NULL
// predicate, so that applying another predicate makes a chain the legacy AST
// could not represent.
func isPredicateChain(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.IsNull:
		return true
	case *syntax.Binary:
		switch e.Op {
		case syntax.OpIn, syntax.OpStartsWith, syntax.OpEndsWith, syntax.OpContains:
			return true
		}
	}
	return false
}

func lowerBinary(e *syntax.Binary) (Expr, error) {
	switch e.Op {
	case syntax.OpOr, syntax.OpXor, syntax.OpAnd:
		l, err := lowerExpr(e.L)
		if err != nil {
			return nil, err
		}
		r, err := lowerExpr(e.R)
		if err != nil {
			return nil, err
		}
		return &BoolExpr{Left: l, Op: string(e.Op), Right: r}, nil
	case syntax.OpAdd, syntax.OpSub:
		l, err := lowerExpr(e.L)
		if err != nil {
			return nil, err
		}
		r, err := lowerExpr(e.R)
		if err != nil {
			return nil, err
		}
		return &ArithExpr{Left: l, Op: string(e.Op), Right: r}, nil
	case syntax.OpMul, syntax.OpDiv, syntax.OpMod, syntax.OpPow:
		return raw(e), nil
	case syntax.OpIn:
		if isPredicateChain(e.L) {
			return raw(e), nil
		}
		list, ok := e.R.(*syntax.ListLit)
		if !ok {
			return raw(e), nil
		}
		base, err := lowerExpr(e.L)
		if err != nil {
			return nil, err
		}
		items := make([]Expr, 0, len(list.Elems))
		for _, el := range list.Elems {
			it, err := lowerExpr(el)
			if err != nil {
				return nil, err
			}
			items = append(items, it)
		}
		return &InListExpr{Expr: base, List: items}, nil
	case syntax.OpStartsWith, syntax.OpEndsWith, syntax.OpContains:
		if isPredicateChain(e.L) {
			return raw(e), nil
		}
		base, err := lowerExpr(e.L)
		if err != nil {
			return nil, err
		}
		pat, err := lowerExpr(e.R)
		if err != nil {
			return nil, err
		}
		return &StringMatchExpr{Expr: base, Pattern: pat, Op: string(e.Op)}, nil
	}
	return nil, unsupported("the %s operator", e.Op)
}

func lowerFuncCall(f *syntax.FuncCall) (Expr, error) {
	if len(f.Namespace) > 0 {
		return raw(f), nil
	}
	name := strings.ToLower(f.Name)
	switch name {
	case "count", "sum", "avg", "min", "max", "collect":
		if f.Star || len(f.Args) == 0 {
			return &AggCallExpr{Func: name, CountStar: name == "count", Distinct: f.Distinct}, nil
		}
		arg, err := lowerExpr(f.Args[0])
		if err != nil {
			return nil, err
		}
		return &AggCallExpr{Func: name, Arg: arg, Distinct: f.Distinct}, nil
	case "exists":
		if len(f.Args) == 0 {
			return raw(f), nil
		}
		inner, err := lowerExpr(f.Args[0])
		if err != nil {
			return nil, err
		}
		if pe, ok := inner.(*PropExpr); ok {
			return &ExistsExpr{Prop: pe}, nil
		}
		return &NullCheckExpr{Expr: inner, IsNotNull: true}, nil
	}
	return raw(f), nil
}

func lowerCase(c *syntax.Case) (Expr, error) {
	ce := &CaseExpr{}
	if c.Subject != nil {
		subj, err := lowerExpr(c.Subject)
		if err != nil {
			return nil, fmt.Errorf("cypher: CASE subject: %w", err)
		}
		ce.Subject = subj
	}
	for _, w := range c.Whens {
		cond, err := lowerExpr(w.Cond)
		if err != nil {
			return nil, fmt.Errorf("cypher: CASE WHEN: %w", err)
		}
		then, err := lowerExpr(w.Then)
		if err != nil {
			return nil, fmt.Errorf("cypher: CASE THEN: %w", err)
		}
		clause := CaseWhenClause{Value: then}
		if c.Subject != nil {
			clause.CaseVal = cond
		} else {
			clause.Condition = cond
		}
		ce.WhenClauses = append(ce.WhenClauses, clause)
	}
	if c.Else != nil {
		el, err := lowerExpr(c.Else)
		if err != nil {
			return nil, fmt.Errorf("cypher: CASE ELSE: %w", err)
		}
		ce.Else = el
	}
	return ce, nil
}

// exprString renders a syntax expression as Cypher-like text for RawExpr. It
// is a diagnostic rendering (children are parenthesised, string literals are
// re-quoted), not the verbatim source.
func exprString(e syntax.Expr) string {
	var sb strings.Builder
	writeExpr(&sb, e)
	return sb.String()
}

func writeExprs(sb *strings.Builder, es []syntax.Expr) {
	for i, e := range es {
		if i > 0 {
			sb.WriteString(", ")
		}
		writeExpr(sb, e)
	}
}

func writeChild(sb *strings.Builder, e syntax.Expr) {
	switch e.(type) {
	case *syntax.Binary, *syntax.Unary, *syntax.Comparison, *syntax.IsNull, *syntax.HasLabels:
		sb.WriteByte('(')
		writeExpr(sb, e)
		sb.WriteByte(')')
	default:
		writeExpr(sb, e)
	}
}

func writeExpr(sb *strings.Builder, e syntax.Expr) {
	switch e := e.(type) {
	case *syntax.Ident:
		sb.WriteString(e.Name)
	case *syntax.Param:
		sb.WriteString("$" + e.Name)
	case *syntax.IntLit:
		sb.WriteString(e.Text)
	case *syntax.FloatLit:
		sb.WriteString(e.Text)
	case *syntax.StringLit:
		sb.WriteString("'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(e.Value) + "'")
	case *syntax.BoolLit:
		sb.WriteString(strconv.FormatBool(e.Value))
	case *syntax.NullLit:
		sb.WriteString("null")
	case *syntax.ListLit:
		sb.WriteByte('[')
		writeExprs(sb, e.Elems)
		sb.WriteByte(']')
	case *syntax.MapLit:
		sb.WriteByte('{')
		for i, en := range e.Entries {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(en.Key + ": ")
			writeExpr(sb, en.Value)
		}
		sb.WriteByte('}')
	case *syntax.Property:
		writeChild(sb, e.Subject)
		sb.WriteString("." + e.Key)
	case *syntax.Subscript:
		writeChild(sb, e.Subject)
		sb.WriteByte('[')
		writeExpr(sb, e.Index)
		sb.WriteByte(']')
	case *syntax.Slice:
		writeChild(sb, e.Subject)
		sb.WriteByte('[')
		if e.From != nil {
			writeExpr(sb, e.From)
		}
		sb.WriteString("..")
		if e.To != nil {
			writeExpr(sb, e.To)
		}
		sb.WriteByte(']')
	case *syntax.Unary:
		op := string(e.Op)
		if e.Op == syntax.OpNot {
			op += " "
		}
		sb.WriteString(op)
		writeChild(sb, e.X)
	case *syntax.Binary:
		writeChild(sb, e.L)
		sb.WriteString(" " + string(e.Op) + " ")
		writeChild(sb, e.R)
	case *syntax.Comparison:
		writeChild(sb, e.Operands[0])
		for i, op := range e.Ops {
			sb.WriteString(" " + string(op) + " ")
			writeChild(sb, e.Operands[i+1])
		}
	case *syntax.IsNull:
		writeChild(sb, e.X)
		if e.Negated {
			sb.WriteString(" IS NOT NULL")
		} else {
			sb.WriteString(" IS NULL")
		}
	case *syntax.HasLabels:
		writeChild(sb, e.X)
		sb.WriteString(":")
		writeLabelExpr(sb, e.Labels)
	case *syntax.FuncCall:
		if len(e.Namespace) > 0 {
			sb.WriteString(strings.Join(e.Namespace, ".") + ".")
		}
		sb.WriteString(e.Name + "(")
		if e.Distinct {
			sb.WriteString("DISTINCT ")
		}
		if e.Star {
			sb.WriteByte('*')
		}
		writeExprs(sb, e.Args)
		sb.WriteByte(')')
	case *syntax.Case:
		sb.WriteString("CASE")
		if e.Subject != nil {
			sb.WriteByte(' ')
			writeExpr(sb, e.Subject)
		}
		for _, w := range e.Whens {
			sb.WriteString(" WHEN ")
			writeExpr(sb, w.Cond)
			sb.WriteString(" THEN ")
			writeExpr(sb, w.Then)
		}
		if e.Else != nil {
			sb.WriteString(" ELSE ")
			writeExpr(sb, e.Else)
		}
		sb.WriteString(" END")
	case *syntax.ListComp:
		sb.WriteString("[" + e.Var + " IN ")
		writeExpr(sb, e.In)
		if e.Where != nil {
			sb.WriteString(" WHERE ")
			writeExpr(sb, e.Where)
		}
		if e.Proj != nil {
			sb.WriteString(" | ")
			writeExpr(sb, e.Proj)
		}
		sb.WriteByte(']')
	case *syntax.Quantifier:
		sb.WriteString(string(e.Kind) + "(" + e.Var + " IN ")
		writeExpr(sb, e.In)
		if e.Where != nil {
			sb.WriteString(" WHERE ")
			writeExpr(sb, e.Where)
		}
		sb.WriteByte(')')
	case *syntax.PatternExpr:
		sb.WriteString("<pattern>")
	case *syntax.PatternComp:
		sb.WriteString("[<pattern> | ")
		writeExpr(sb, e.Proj)
		sb.WriteByte(']')
	default:
		fmt.Fprintf(sb, "<%T>", e)
	}
}

// writeLabelExpr renders a label expression; a conjunction of names uses the
// legacy `A:B` spelling.
func writeLabelExpr(sb *strings.Builder, le syntax.LabelExpr) {
	switch le := le.(type) {
	case *syntax.LabelName:
		if le.Dynamic != nil {
			sb.WriteString("$(")
			writeExpr(sb, le.Dynamic)
			sb.WriteByte(')')
		} else {
			sb.WriteString(le.Name)
		}
	case *syntax.LabelWildcard:
		sb.WriteByte('%')
	case *syntax.LabelNot:
		sb.WriteByte('!')
		writeLabelExpr(sb, le.X)
	case *syntax.LabelAnd:
		writeLabelExpr(sb, le.L)
		sb.WriteByte(':')
		writeLabelExpr(sb, le.R)
	case *syntax.LabelOr:
		writeLabelExpr(sb, le.L)
		sb.WriteByte('|')
		writeLabelExpr(sb, le.R)
	}
}
