package interp

import (
	"context"
	"fmt"
	"strings"
)

// ValidateImported checks entities that were written behind the interpreter's
// back (the bulk importer inserts through the store) against the schema, as if
// a statement had just created them: constraints and vector index dimensions.
// db must see the uncommitted rows, i.e. be the importer's own transaction. The
// first violation is returned as an *Error with a SchemaRef.
func (e *Engine) ValidateImported(ctx context.Context, db DB, nodeIDs, relIDs []int64) error {
	const batch = 500
	for lo := 0; lo < len(nodeIDs); lo += batch {
		hi := min(lo+batch, len(nodeIDs))
		g := newGraph(ctx, db)
		g.eng = e
		if err := g.preloadNodes(nodeIDs[lo:hi]); err != nil {
			return err
		}
		for _, id := range nodeIDs[lo:hi] {
			n, err := g.node(id)
			if err != nil {
				return err
			}
			g.createdNodes[n] = true
		}
		if err := g.checkConstraints(); err != nil {
			return err
		}
		if err := g.checkVectorIndexes(); err != nil {
			return err
		}
	}
	for lo := 0; lo < len(relIDs); lo += batch {
		hi := min(lo+batch, len(relIDs))
		g := newGraph(ctx, db)
		g.eng = e
		if err := g.preloadRels(relIDs[lo:hi]); err != nil {
			return err
		}
		for _, id := range relIDs[lo:hi] {
			if r := g.rels[id]; r != nil {
				g.createdRels[r] = true
			}
		}
		if err := g.checkConstraints(); err != nil {
			return err
		}
		if err := g.checkVectorIndexes(); err != nil {
			return err
		}
	}
	return nil
}

// InvalidateVectors forgets every cached vector matrix. Call it after data
// changed outside the interpreter (a bulk import); the matrices are rebuilt on
// the next search.
func (e *Engine) InvalidateVectors() {
	if e == nil {
		return
	}
	e.vecMu.Lock()
	defer e.vecMu.Unlock()
	e.vec.matrices = nil
	e.vec.epoch++
}

// preloadRels loads the given relationships into the cache with one query.
func (g *graph) preloadRels(ids []int64) error {
	var missing []any
	for _, id := range ids {
		if _, ok := g.rels[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	rows, err := g.db.QueryContext(g.ctx,
		`SELECT id, type, start_id, end_id, props FROM edges WHERE id IN (?`+strings.Repeat(",?", len(missing)-1)+`)`, missing...)
	if err != nil {
		return err
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
			return err
		}
		recs = append(recs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, r := range recs {
		if _, err := g.internRel(r.id, r.typ, r.start, r.end, r.props); err != nil {
			return err
		}
	}
	return nil
}

func quoteName(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func targetPattern(d schemaDef) string {
	if d.Entity == "RELATIONSHIP" {
		return "()-[r:" + quoteName(d.Targets[0]) + "]-()"
	}
	return "(n:" + quoteName(d.Targets[0]) + ")"
}

func propList(d schemaDef, v string) string {
	parts := make([]string, len(d.Props))
	for i, p := range d.Props {
		parts[i] = v + "." + quoteName(p)
	}
	return strings.Join(parts, ", ")
}

// SchemaStatements returns Cypher that recreates the user-defined indexes and
// constraints (not the built-in token lookup indexes), in creation order, with
// IF NOT EXISTS so replaying them is harmless. Index kinds graphlite cannot
// recreate from their definition (full-text) are left out.
func (e *Engine) SchemaStatements(ctx context.Context, db DB) ([]string, error) {
	g := newGraph(ctx, db)
	defs, err := g.loadSchema()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range defs {
		v := "n"
		if d.Entity == "RELATIONSHIP" {
			v = "r"
		}
		name := quoteName(d.Name)
		if !d.Constraint {
			switch d.Kind {
			case "RANGE", "TEXT", "POINT":
				out = append(out, fmt.Sprintf("CREATE %s INDEX %s IF NOT EXISTS FOR %s ON (%s)", d.Kind, name, targetPattern(d), propList(d, v)))
			case "VECTOR":
				out = append(out, fmt.Sprintf("CREATE VECTOR INDEX %s IF NOT EXISTS FOR %s ON (%s) OPTIONS {indexConfig: {`vector.dimensions`: %d, `vector.similarity_function`: '%s'}}",
					name, targetPattern(d), propList(d, v), d.VectorDims, d.VectorSim))
			}
			continue
		}
		props := propList(d, v)
		if len(d.Props) > 1 {
			props = "(" + props + ")"
		}
		var req string
		switch d.Kind {
		case "UNIQUENESS":
			req = "IS UNIQUE"
		case "KEY":
			req = "IS NODE KEY"
			if d.Entity == "RELATIONSHIP" {
				req = "IS RELATIONSHIP KEY"
			}
		case "EXISTENCE":
			req = "IS NOT NULL"
		case "TYPE":
			req = "IS :: " + d.ValueType
		default:
			continue
		}
		out = append(out, fmt.Sprintf("CREATE CONSTRAINT %s IF NOT EXISTS FOR %s REQUIRE %s %s", name, targetPattern(d), props, req))
	}
	return out, nil
}
