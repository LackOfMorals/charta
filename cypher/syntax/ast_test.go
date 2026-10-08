package syntax

import "testing"

// Compile-time proof that each node type satisfies the interface the grammar
// checklist says it belongs to.
var (
	_ Body = (*SingleQuery)(nil)
	_ Body = (*UnionQuery)(nil)
	_ Body = (*NextQuery)(nil)
	_ Body = (*CreateIndex)(nil)
	_ Body = (*CreateConstraint)(nil)
	_ Body = (*DropSchema)(nil)
	_ Body = (*Show)(nil)
	_ Body = (*ServerCommand)(nil)
	_ Body = (*Conditional)(nil)

	_ Clause = (*Match)(nil)
	_ Clause = (*Unwind)(nil)
	_ Clause = (*Create)(nil)
	_ Clause = (*Insert)(nil)
	_ Clause = (*Merge)(nil)
	_ Clause = (*Set)(nil)
	_ Clause = (*Remove)(nil)
	_ Clause = (*Delete)(nil)
	_ Clause = (*Foreach)(nil)
	_ Clause = (*Call)(nil)
	_ Clause = (*CallSubquery)(nil)
	_ Clause = (*LoadCSV)(nil)
	_ Clause = (*With)(nil)
	_ Clause = (*Return)(nil)
	_ Clause = (*OrderSkipLimit)(nil)
	_ Clause = (*Filter)(nil)
	_ Clause = (*Let)(nil)
	_ Clause = (*Finish)(nil)
	_ Clause = (*Conditional)(nil)

	_ PatternElem = (*NodePattern)(nil)
	_ PatternElem = (*RelPattern)(nil)
	_ PatternElem = (*GroupPattern)(nil)

	_ LabelExpr = (*LabelName)(nil)
	_ LabelExpr = (*LabelWildcard)(nil)
	_ LabelExpr = (*LabelNot)(nil)
	_ LabelExpr = (*LabelAnd)(nil)
	_ LabelExpr = (*LabelOr)(nil)

	_ Expr = (*IntLit)(nil)
	_ Expr = (*FloatLit)(nil)
	_ Expr = (*StringLit)(nil)
	_ Expr = (*BoolLit)(nil)
	_ Expr = (*NullLit)(nil)
	_ Expr = (*ListLit)(nil)
	_ Expr = (*MapLit)(nil)
	_ Expr = (*Param)(nil)
	_ Expr = (*Ident)(nil)
	_ Expr = (*Property)(nil)
	_ Expr = (*Subscript)(nil)
	_ Expr = (*Slice)(nil)
	_ Expr = (*Unary)(nil)
	_ Expr = (*Binary)(nil)
	_ Expr = (*Comparison)(nil)
	_ Expr = (*IsNull)(nil)
	_ Expr = (*TypePredicate)(nil)
	_ Expr = (*Normalized)(nil)
	_ Expr = (*HasLabels)(nil)
	_ Expr = (*FuncCall)(nil)
	_ Expr = (*Case)(nil)
	_ Expr = (*ListComp)(nil)
	_ Expr = (*PatternComp)(nil)
	_ Expr = (*Quantifier)(nil)
	_ Expr = (*Reduce)(nil)
	_ Expr = (*SubqueryExpr)(nil)
	_ Expr = (*PatternExpr)(nil)
	_ Expr = (*MapProjection)(nil)
)

func TestDump(t *testing.T) {
	n := &Return{Projection: Projection{
		Distinct: true,
		Items: []ProjectionItem{{
			Expr: &Comparison{
				Operands: []Expr{&Ident{Name: "a"}, &IntLit{Value: 1, Text: "1"}},
				Ops:      []CompareOp{CmpLt},
			},
			Alias: "ok",
		}},
		Limit: &IntLit{Value: 2, Text: "2"},
	}}
	want := `(Return Distinct:true Items:[(ProjectionItem Expr:(Comparison Operands:[(Ident Name:"a") (IntLit Value:1 Text:"1")] Ops:["<"]) Alias:"ok")] Limit:(IntLit Value:2 Text:"2"))`
	if got := Dump(n); got != want {
		t.Errorf("Dump:\n got %s\nwant %s", got, want)
	}
}

func TestLocPos(t *testing.T) {
	n := &Ident{Loc: Loc{At: Pos{Offset: 4, Line: 2, Col: 3}}, Name: "x"}
	if got := n.Pos(); got.Line != 2 || got.Col != 3 {
		t.Errorf("Pos() = %v", got)
	}
	if Dump(n) != `(Ident Name:"x")` {
		t.Errorf("Dump must omit positions, got %s", Dump(n))
	}
}
