package api

import (
	"github.com/graingraph/graingraph/cypher/syntax"
)

// queryTypeOf classifies a statement the way the Query API reports it:
//
//	"s"  schema write (CREATE/DROP INDEX or CONSTRAINT)
//	"rw" a write that returns rows (e.g. CREATE … RETURN n)
//	"w"  a write that returns nothing (e.g. MATCH … DELETE)
//	"r"  read only (everything else)
//
// Statements that fail to parse default to "r"; the execution error
// that follows dominates the response anyway.
func queryTypeOf(statement string) string {
	st, err := syntax.Parse(statement)
	if err != nil {
		return "r"
	}
	var c classify
	c.visitBody(st.Body)
	switch {
	case c.schema:
		return "s"
	case c.write && c.returns:
		return "rw"
	case c.write:
		return "w"
	default:
		return "r"
	}
}

// classify accumulates classification facts across a statement body.
type classify struct {
	schema  bool
	write   bool
	returns bool
}

func (c *classify) visitBody(body syntax.Body) {
	switch b := body.(type) {
	case *syntax.SingleQuery:
		for _, cl := range b.Clauses {
			c.visitClause(cl)
		}
	case *syntax.UnionQuery:
		for _, q := range b.Queries {
			for _, cl := range q.Clauses {
				c.visitClause(cl)
			}
		}
	case *syntax.Conditional:
		for _, br := range b.Branches {
			c.visitBody(br.Body)
		}
		if b.Else != nil {
			c.visitBody(b.Else)
		}
	case *syntax.CreateIndex, *syntax.CreateConstraint, *syntax.DropSchema:
		c.schema = true
	}
}

func (c *classify) visitClause(cl syntax.Clause) {
	switch cl := cl.(type) {
	case *syntax.Return:
		// A write that returns rows is reported as read-write.
		c.returns = true
	case *syntax.Create, *syntax.Merge, *syntax.Set, *syntax.Remove, *syntax.Delete:
		c.write = true
	case *syntax.Foreach:
		c.write = true
		for _, inner := range cl.Body {
			c.visitClause(inner)
		}
	case *syntax.CallSubquery:
		c.visitBody(cl.Body)
	}
}

// columnsOf returns the result columns a statement declares: the alias,
// or the source text, of each item of its final RETURN. It returns nil
// when they cannot be known without running the statement (RETURN *, a
// statement without RETURN, or one that does not parse).
func columnsOf(statement string) []string {
	st, err := syntax.Parse(statement)
	if err != nil {
		return nil
	}
	var clauses []syntax.Clause
	switch b := st.Body.(type) {
	case *syntax.SingleQuery:
		clauses = b.Clauses
	case *syntax.UnionQuery:
		if len(b.Queries) == 0 {
			return nil
		}
		clauses = b.Queries[0].Clauses
	}
	if len(clauses) == 0 {
		return nil
	}
	ret, ok := clauses[len(clauses)-1].(*syntax.Return)
	if !ok || ret.Star {
		return nil
	}
	cols := make([]string, len(ret.Items))
	for i, it := range ret.Items {
		cols[i] = it.Alias
		if cols[i] == "" {
			cols[i] = it.Source
		}
	}
	return cols
}
