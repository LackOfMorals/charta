package interp

import (
	"sort"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

var (
	indexColumns      = []string{"id", "name", "state", "populationPercent", "type", "entityType", "labelsOrTypes", "properties", "indexProvider", "owningConstraint", "lastRead", "readCount"}
	constraintColumns = []string{"id", "name", "type", "entityType", "labelsOrTypes", "properties", "ownedIndex", "propertyType"}
	procedureColumns  = []string{"name", "description", "mode", "worksOnSystem"}
)

func strList(ss []string) any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

var indexProviders = map[string]string{
	"RANGE": "range-1.0", "TEXT": "text-2.0", "POINT": "point-1.0",
	"LOOKUP": "token-lookup-1.0", "FULLTEXT": "fulltext-1.0", "VECTOR": "vector-2.0",
}

// execShow runs SHOW INDEXES|CONSTRAINTS|PROCEDURES [YIELD … [WHERE …]] [WHERE …].
func (ex *exec) execShow(sh *syntax.Show) ([]string, []row, error) {
	words := strings.Fields(sh.What)
	if len(words) > 0 && words[0] == "ALL" {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil, nil, unsupported("SHOW %s", sh.What)
	}
	last := words[len(words)-1]
	filter := words[:len(words)-1]

	var cols []string
	var rows []row
	var err error
	switch last {
	case "INDEX", "INDEXES":
		cols = indexColumns
		rows, err = ex.showIndexes(filter, sh.What)
	case "CONSTRAINT", "CONSTRAINTS":
		cols = constraintColumns
		rows, err = ex.showConstraints(filter, sh.What)
	case "PROCEDURE", "PROCEDURES":
		cols = procedureColumns
		rows, err = ex.showProcedures()
	default:
		return nil, nil, unsupported("SHOW %s", sh.What)
	}
	if err != nil {
		return nil, nil, err
	}

	// WHERE and YIELD apply to the rows with the column names as variables.
	filterRows := func(where syntax.Expr) error {
		var kept []row
		for _, r := range rows {
			c, err := ex.eval(where, r)
			if err != nil {
				return err
			}
			if c == true {
				kept = append(kept, r)
			}
		}
		rows = kept
		return nil
	}
	if sh.Where != nil {
		if err := filterRows(sh.Where); err != nil {
			return nil, nil, err
		}
	}
	if y := sh.Yield; y != nil {
		if y.Where != nil {
			if err := filterRows(y.Where); err != nil {
				return nil, nil, err
			}
		}
		if !y.Star {
			var out []string
			mapping := map[string]string{}
			for _, it := range y.Items {
				if !containsStr(cols, it.Name) {
					return nil, nil, errorf("SyntaxError", "UndefinedVariable", "SHOW has no column `%s`", it.Name)
				}
				name := it.Alias
				if name == "" {
					name = it.Name
				}
				out = append(out, name)
				mapping[name] = it.Name
			}
			projected := make([]row, len(rows))
			for i, r := range rows {
				nr := make(row, len(out))
				for _, name := range out {
					nr[name] = r[mapping[name]]
				}
				projected[i] = nr
			}
			cols, rows = out, projected
		}
	}
	if sh.Return != nil {
		st := &qstate{rows: rows}
		st.declare(cols...)
		names, err := ex.projectionNames(&sh.Return.Projection, st)
		if err != nil {
			return nil, nil, err
		}
		if err := ex.projection(&sh.Return.Projection, nil, st, true); err != nil {
			return nil, nil, err
		}
		return names, st.rows, nil
	}
	return cols, rows, nil
}

func (ex *exec) showIndexes(filter []string, what string) ([]row, error) {
	kind := ""
	if len(filter) == 1 {
		kind = filter[0]
	} else if len(filter) > 1 {
		return nil, unsupported("SHOW %s", what)
	}
	defs, err := ex.g.loadSchema()
	if err != nil {
		return nil, err
	}
	var rows []row
	add := func(id int64, name, typ, entity string, targets, props []string, provider string, owning any) {
		if kind != "" && kind != typ {
			return
		}
		rows = append(rows, row{
			"id": id, "name": name, "state": "ONLINE", "populationPercent": 100.0, "type": typ,
			"entityType": entity, "labelsOrTypes": nullableList(targets), "properties": nullableList(props),
			"indexProvider": provider, "owningConstraint": owning, "lastRead": nil, "readCount": int64(0),
		})
	}
	// The token lookup indexes every database has.
	add(1, "node_label_lookup_index", "LOOKUP", "NODE", nil, nil, indexProviders["LOOKUP"], nil)
	add(2, "relationship_type_lookup_index", "LOOKUP", "RELATIONSHIP", nil, nil, indexProviders["LOOKUP"], nil)
	for _, d := range defs {
		switch {
		case !d.Constraint:
			add(d.ID+2, d.Name, d.Kind, d.Entity, d.Targets, d.Props, indexProviders[d.Kind], nil)
		case d.Kind == "UNIQUENESS" || d.Kind == "KEY":
			add(d.ID+2, d.Name, "RANGE", d.Entity, d.Targets, d.Props, indexProviders["RANGE"], d.Name)
		}
	}
	return rows, nil
}

func nullableList(ss []string) any {
	if len(ss) == 0 {
		return nil
	}
	return strList(ss)
}

func constraintTypeName(d schemaDef) string {
	prefix := "NODE_"
	if d.Entity == "RELATIONSHIP" {
		prefix = "RELATIONSHIP_"
	}
	switch d.Kind {
	case "UNIQUENESS":
		if d.Entity == "RELATIONSHIP" {
			return "RELATIONSHIP_UNIQUENESS"
		}
		return "UNIQUENESS"
	case "KEY":
		return prefix + "KEY"
	case "EXISTENCE":
		return prefix + "PROPERTY_EXISTENCE"
	}
	return prefix + "PROPERTY_TYPE"
}

func (ex *exec) showConstraints(filter []string, what string) ([]row, error) {
	want := strings.Join(filter, " ")
	match := func(d schemaDef) bool {
		t := constraintTypeName(d)
		switch want {
		case "":
			return true
		case "UNIQUE", "UNIQUENESS":
			return strings.HasSuffix(t, "UNIQUENESS")
		case "NODE KEY", "KEY", "RELATIONSHIP KEY":
			return strings.HasSuffix(t, "KEY")
		case "EXIST", "EXISTENCE", "PROPERTY EXISTENCE", "NODE EXIST", "NODE PROPERTY EXISTENCE", "RELATIONSHIP EXIST", "RELATIONSHIP PROPERTY EXISTENCE":
			return strings.HasSuffix(t, "EXISTENCE")
		case "TYPE", "PROPERTY TYPE", "NODE PROPERTY TYPE", "RELATIONSHIP PROPERTY TYPE":
			return strings.HasSuffix(t, "TYPE")
		}
		return false
	}
	switch want {
	case "", "UNIQUE", "UNIQUENESS", "NODE KEY", "KEY", "RELATIONSHIP KEY", "EXIST", "EXISTENCE", "PROPERTY EXISTENCE",
		"NODE EXIST", "NODE PROPERTY EXISTENCE", "RELATIONSHIP EXIST", "RELATIONSHIP PROPERTY EXISTENCE",
		"TYPE", "PROPERTY TYPE", "NODE PROPERTY TYPE", "RELATIONSHIP PROPERTY TYPE":
	default:
		return nil, unsupported("SHOW %s", what)
	}
	defs, err := ex.g.loadSchema()
	if err != nil {
		return nil, err
	}
	var rows []row
	for _, d := range defs {
		if !d.Constraint || !match(d) {
			continue
		}
		var owned, ptype any
		if d.Kind == "UNIQUENESS" || d.Kind == "KEY" {
			owned = d.Name
		}
		if d.Kind == "TYPE" {
			ptype = d.ValueType
		}
		rows = append(rows, row{
			"id": d.ID + 2, "name": d.Name, "type": constraintTypeName(d), "entityType": d.Entity,
			"labelsOrTypes": strList(d.Targets), "properties": strList(d.Props), "ownedIndex": owned, "propertyType": ptype,
		})
	}
	return rows, nil
}

func (ex *exec) showProcedures() ([]row, error) {
	names := map[string]bool{}
	for _, n := range []string{"db.labels", "db.relationshipTypes", "db.propertyKeys"} {
		names[n] = true
	}
	if ex.procs != nil {
		for _, n := range ex.procs.Names() {
			names[n] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	rows := make([]row, len(sorted))
	for i, n := range sorted {
		rows[i] = row{"name": n, "description": "", "mode": "READ", "worksOnSystem": false}
	}
	return rows, nil
}
