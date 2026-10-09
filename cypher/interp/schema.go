package interp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// Schema commands (CREATE/DROP INDEX|CONSTRAINT, SHOW INDEXES|CONSTRAINTS).
//
// Definitions live in the graphlite_schema table. A RANGE/TEXT index and the
// lookup index behind a uniqueness constraint are SQLite expression indexes on
// json_extract(props, '$."key"'); the other index kinds are recorded so SHOW
// reports them, but queries do not need them to be correct. Constraints are
// enforced by the interpreter when a statement finishes, over the entities the
// statement created or changed.

const schemaTable = "graphlite_schema"

// schemaDef is one index or constraint.
type schemaDef struct {
	ID         int64    `json:"-"`
	Name       string   `json:"-"`
	Constraint bool     `json:"constraint"`
	Kind       string   `json:"kind"`   // RANGE, TEXT, POINT, LOOKUP, FULLTEXT, VECTOR / UNIQUENESS, KEY, EXISTENCE, TYPE
	Entity     string   `json:"entity"` // NODE or RELATIONSHIP
	Targets    []string `json:"targets"`
	Props      []string `json:"props"`
	ValueType  string   `json:"valueType,omitempty"`
	Options    string   `json:"options,omitempty"`
	BackingIdx string   `json:"backingIndex,omitempty"`
}

func (ex *exec) schemaExists() (bool, error) {
	var n int
	err := ex.g.queryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, []any{schemaTable}, &n)
	return n > 0, err
}

func (ex *exec) ensureSchemaTable() error {
	_, err := ex.g.db.ExecContext(ex.g.ctx, `CREATE TABLE IF NOT EXISTS `+schemaTable+` (
		id   INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		def  TEXT NOT NULL)`)
	return err
}

// loadSchema reads every definition, in creation order.
func (g *graph) loadSchema() ([]schemaDef, error) {
	var exists int
	if err := g.queryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, []any{schemaTable}, &exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	rows, err := g.db.QueryContext(g.ctx, `SELECT id, name, def FROM `+schemaTable+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schemaDef
	for rows.Next() {
		var d schemaDef
		var raw string
		if err := rows.Scan(&d.ID, &d.Name, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return nil, fmt.Errorf("corrupt schema definition %q: %w", d.Name, err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// constraintsFor returns the constraints, loaded once per statement.
func (g *graph) constraintsFor() ([]schemaDef, error) {
	if g.constraintsLoaded {
		return g.constraints, nil
	}
	all, err := g.loadSchema()
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.Constraint {
			g.constraints = append(g.constraints, d)
		}
	}
	g.constraintsLoaded = true
	return g.constraints, nil
}

func schemaError(code, format string, args ...any) error {
	return errorf("SchemaError", code, format, args...)
}

// ─── CREATE ─────────────────────────────────────────────────────────────────

// target extracts the entity kind, label/type name and bound variable of the
// pattern an index or constraint is FOR.
func schemaTarget(t syntax.PatternElem) (entity, name, variable string, err error) {
	var le syntax.LabelExpr
	switch t := t.(type) {
	case *syntax.NodePattern:
		entity, variable, le = "NODE", t.Var, t.Labels
	case *syntax.RelPattern:
		entity, variable, le = "RELATIONSHIP", t.Var, t.Types
	default:
		return "", "", "", unsupported("schema target %T", t)
	}
	if le == nil {
		return entity, "", variable, nil // LOOKUP indexes cover every label/type
	}
	ln, ok := le.(*syntax.LabelName)
	if !ok || ln.Dynamic != nil {
		return "", "", "", unsupported("a schema command over more than one plain label or type")
	}
	return entity, ln.Name, variable, nil
}

func schemaProps(exprs []syntax.Expr, variable string) ([]string, error) {
	var out []string
	for _, e := range exprs {
		p, ok := e.(*syntax.Property)
		if !ok {
			return nil, unsupported("an index or constraint over %T (only n.property is supported)", e)
		}
		id, ok := p.Subject.(*syntax.Ident)
		if !ok || id.Name != variable {
			return nil, schemaError("InvalidSchemaTarget", "properties must be accessed on the pattern variable `%s`", variable)
		}
		out = append(out, p.Key)
	}
	return out, nil
}

func (ex *exec) optionsText(e syntax.Expr) (string, error) {
	if e == nil {
		return "", nil
	}
	v, err := ex.eval(e, row{})
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", schemaError("InvalidOptions", "OPTIONS must be a map of plain values")
	}
	return string(b), nil
}

func (ex *exec) execCreateIndex(ci *syntax.CreateIndex) error {
	entity, name, variable, err := schemaTarget(ci.Target)
	if err != nil {
		return err
	}
	kind := ci.Kind
	if kind == "" {
		kind = "RANGE"
	}
	var props []string
	if kind != "LOOKUP" {
		if props, err = schemaProps(ci.Properties, variable); err != nil {
			return err
		}
		if len(props) == 0 {
			return schemaError("InvalidSchemaTarget", "an index needs at least one property")
		}
		if name == "" {
			return schemaError("InvalidSchemaTarget", "an index needs a label or relationship type")
		}
	}
	opts, err := ex.optionsText(ci.Options)
	if err != nil {
		return err
	}
	def := schemaDef{Kind: kind, Entity: entity, Props: props, Options: opts}
	if name != "" {
		def.Targets = []string{name}
	}
	return ex.addSchema(ci.Name, ci.IfNotExists, "index", def)
}

func (ex *exec) execCreateConstraint(cc *syntax.CreateConstraint) error {
	entity, name, variable, err := schemaTarget(cc.Target)
	if err != nil {
		return err
	}
	if name == "" {
		return schemaError("InvalidSchemaTarget", "a constraint needs a label or relationship type")
	}
	props, err := schemaProps(cc.Properties, variable)
	if err != nil {
		return err
	}
	opts, err := ex.optionsText(cc.Options)
	if err != nil {
		return err
	}
	def := schemaDef{Constraint: true, Entity: entity, Targets: []string{name}, Props: props, Options: opts}
	switch cc.Kind {
	case syntax.ConstraintUnique:
		def.Kind = "UNIQUENESS"
	case syntax.ConstraintKey:
		def.Kind = "KEY"
	case syntax.ConstraintNotNull:
		def.Kind = "EXISTENCE"
	case syntax.ConstraintType:
		def.Kind = "TYPE"
		def.ValueType = cc.ValueType
	}
	if len(props) > 1 && (def.Kind == "EXISTENCE" || def.Kind == "TYPE") {
		return schemaError("InvalidSchemaTarget", "this kind of constraint takes a single property")
	}
	// A new constraint must hold for the data already stored.
	if err := ex.validateExisting(def); err != nil {
		return err
	}
	return ex.addSchema(cc.Name, cc.IfNotExists, "constraint", def)
}

// addSchema records a definition and creates its backing SQLite index.
func (ex *exec) addSchema(name string, ifNotExists bool, what string, def schemaDef) error {
	if err := ex.ensureSchemaTable(); err != nil {
		return err
	}
	existing, err := ex.g.loadSchema()
	if err != nil {
		return err
	}
	for _, d := range existing {
		same := d.Constraint == def.Constraint && d.Kind == def.Kind && d.Entity == def.Entity &&
			strings.Join(d.Targets, ",") == strings.Join(def.Targets, ",") && strings.Join(d.Props, ",") == strings.Join(def.Props, ",")
		if name != "" && d.Name == name || (name == "" && same) {
			if ifNotExists {
				return nil
			}
			if name != "" && d.Name == name {
				return schemaError("EquivalentSchemaRuleAlreadyExists", "a %s named `%s` already exists", what, name)
			}
			return schemaError("EquivalentSchemaRuleAlreadyExists", "an equivalent %s already exists (`%s`)", what, d.Name)
		}
		if same && name != "" {
			if ifNotExists {
				return nil
			}
			return schemaError("EquivalentSchemaRuleAlreadyExists", "an equivalent %s already exists (`%s`)", what, d.Name)
		}
	}
	// Reserve the row to learn the id, then fill in the generated name.
	if name == "" {
		name = "pending" + strconv.FormatInt(int64(len(existing))+1, 10)
	}
	raw, _ := json.Marshal(def)
	res, err := ex.g.db.ExecContext(ex.g.ctx, `INSERT INTO `+schemaTable+` (name, def) VALUES (?, ?)`, name, string(raw))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if strings.HasPrefix(name, "pending") {
		name = fmt.Sprintf("%s_%d", map[bool]string{false: "index", true: "constraint"}[def.Constraint], id)
		if _, err := ex.g.db.ExecContext(ex.g.ctx, `UPDATE `+schemaTable+` SET name = ? WHERE id = ?`, name, id); err != nil {
			return err
		}
	}
	if idx := backingIndexSQL(id, def); idx != "" && (def.Kind == "RANGE" || def.Kind == "TEXT" || def.Kind == "UNIQUENESS" || def.Kind == "KEY") {
		if _, err := ex.g.db.ExecContext(ex.g.ctx, idx); err != nil {
			return err
		}
		def.BackingIdx = backingIndexName(id)
		raw, _ := json.Marshal(def)
		if _, err := ex.g.db.ExecContext(ex.g.ctx, `UPDATE `+schemaTable+` SET def = ? WHERE id = ?`, string(raw), id); err != nil {
			return err
		}
	}
	if def.Constraint {
		ex.g.counters.ConstraintsAdded++
		ex.g.constraintsLoaded = false
		ex.g.constraints = nil
	} else {
		ex.g.counters.IndexesAdded++
	}
	return nil
}

func backingIndexName(id int64) string { return "gl_idx_" + strconv.FormatInt(id, 10) }

// backingIndexSQL is the CREATE INDEX statement for a definition, or "" when
// a property key is not a plain identifier (the definition is still recorded).
func backingIndexSQL(id int64, d schemaDef) string {
	var exprs []string
	for _, p := range d.Props {
		if !pushableKey(p) {
			return ""
		}
		exprs = append(exprs, `json_extract(props, '$."`+p+`"')`)
	}
	if len(exprs) == 0 {
		return ""
	}
	table := "nodes"
	if d.Entity == "RELATIONSHIP" {
		table = "edges"
	}
	return `CREATE INDEX IF NOT EXISTS ` + backingIndexName(id) + ` ON ` + table + `(` + strings.Join(exprs, ", ") + `)`
}

// ─── DROP ───────────────────────────────────────────────────────────────────

func (ex *exec) execDropSchema(ds *syntax.DropSchema) error {
	defs, err := ex.g.loadSchema()
	if err != nil {
		return err
	}
	for _, d := range defs {
		if d.Name != ds.Name {
			continue
		}
		if d.Constraint != ds.Constraint {
			what := "an index"
			if d.Constraint {
				what = "a constraint"
			}
			return schemaError("SchemaRuleNotFound", "`%s` is %s, not a %s", ds.Name, what, map[bool]string{false: "index", true: "constraint"}[ds.Constraint])
		}
		if d.BackingIdx != "" {
			if _, err := ex.g.db.ExecContext(ex.g.ctx, `DROP INDEX IF EXISTS `+d.BackingIdx); err != nil {
				return err
			}
		}
		if _, err := ex.g.db.ExecContext(ex.g.ctx, `DELETE FROM `+schemaTable+` WHERE id = ?`, d.ID); err != nil {
			return err
		}
		if d.Constraint {
			ex.g.counters.ConstraintsRemoved++
			ex.g.constraintsLoaded = false
			ex.g.constraints = nil
		} else {
			ex.g.counters.IndexesRemoved++
		}
		return nil
	}
	if ds.IfExists {
		return nil
	}
	what := "index"
	if ds.Constraint {
		what = "constraint"
	}
	return schemaError("SchemaRuleNotFound", "no %s named `%s`", what, ds.Name)
}

// ─── enforcement ────────────────────────────────────────────────────────────

func describeEntity(entity string, id int64) string {
	if entity == "NODE" {
		return fmt.Sprintf("Node(%d)", id)
	}
	return fmt.Sprintf("Relationship(%d)", id)
}

func constraintViolation(format string, args ...any) error {
	return errorf("ConstraintVerificationFailed", "ConstraintValidationFailed", format, args...)
}

// checkOne verifies a single entity against one constraint (uniqueness is
// checked separately because it needs the rest of the graph).
func checkOne(d schemaDef, id int64, props map[string]any) error {
	who := describeEntity(d.Entity, id)
	target := d.Targets[0]
	switch d.Kind {
	case "EXISTENCE", "KEY":
		for _, p := range d.Props {
			if props[p] == nil {
				return constraintViolation("%s with %s `%s` must have the property `%s`", who, map[string]string{"NODE": "label", "RELATIONSHIP": "type"}[d.Entity], target, p)
			}
		}
	case "TYPE":
		p := d.Props[0]
		if v := props[p]; v != nil && !matchesVTypes(v, parseVTypes(d.ValueType)) {
			return constraintViolation("%s with %s `%s` requires the property `%s` to be of type %s, but it was %s",
				who, map[string]string{"NODE": "label", "RELATIONSHIP": "type"}[d.Entity], target, p, d.ValueType, valueTypeName(v, true))
		}
	}
	return nil
}

// tupleOf returns the constrained property values of an entity, or ok=false if
// any is null (a null never conflicts).
func tupleOf(d schemaDef, props map[string]any) ([]any, bool) {
	out := make([]any, len(d.Props))
	for i, p := range d.Props {
		if props[p] == nil {
			return nil, false
		}
		out[i] = props[p]
	}
	return out, true
}

// findDuplicate looks for another entity with the same constrained values.
func (g *graph) findDuplicate(d schemaDef, id int64, tuple []any) (int64, error) {
	table := "nodes"
	label := `EXISTS (SELECT 1 FROM node_labels l WHERE l.node_id = e.id AND l.label = ?)`
	if d.Entity == "RELATIONSHIP" {
		table, label = "edges", `e.type = ?`
	}
	var sb strings.Builder
	args := []any{id, d.Targets[0]}
	sb.WriteString(`SELECT e.id, e.props FROM ` + table + ` e WHERE e.id <> ? AND ` + label)
	scalar := true
	for i, p := range d.Props {
		switch tuple[i].(type) {
		case string, int64, float64:
			if pushableKey(p) {
				sb.WriteString(` AND json_extract(e.props, '$."` + p + `"') = ?`)
				args = append(args, tuple[i])
				continue
			}
		}
		scalar = false
	}
	rows, err := g.db.QueryContext(g.ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var other int64
		var raw string
		if err := rows.Scan(&other, &raw); err != nil {
			return 0, err
		}
		if scalar {
			return other, nil
		}
		props, err := decodeProps(raw)
		if err != nil {
			return 0, err
		}
		same := true
		for i, p := range d.Props {
			if equals(props[p], tuple[i]) != triTrue {
				same = false
				break
			}
		}
		if same {
			return other, nil
		}
	}
	return 0, rows.Err()
}

// checkConstraints validates the entities the statement created or changed.
func (g *graph) checkConstraints() error {
	if len(g.createdNodes)+len(g.nodeSnap)+len(g.createdRels)+len(g.relSnap) == 0 {
		return nil
	}
	cons, err := g.constraintsFor()
	if err != nil || len(cons) == 0 {
		return err
	}
	check := func(entity string, id int64, labels []string, props map[string]any) error {
		for _, d := range cons {
			if d.Entity != entity || !containsStr(labels, d.Targets[0]) {
				continue
			}
			if err := checkOne(d, id, props); err != nil {
				return err
			}
			if d.Kind == "UNIQUENESS" || d.Kind == "KEY" {
				tuple, ok := tupleOf(d, props)
				if !ok {
					continue
				}
				other, err := g.findDuplicate(d, id, tuple)
				if err != nil {
					return err
				}
				if other != 0 {
					vals := make([]string, len(tuple))
					for i, v := range tuple {
						vals[i] = fmt.Sprintf("`%s` = %s", d.Props[i], renderForError(v))
					}
					return constraintViolation("%s already exists with %s `%s` and property %s",
						describeEntity(entity, other), map[string]string{"NODE": "label", "RELATIONSHIP": "type"}[entity], d.Targets[0], strings.Join(vals, ", "))
				}
			}
		}
		return nil
	}
	touchedNodes := map[*Node]bool{}
	for n := range g.createdNodes {
		touchedNodes[n] = true
	}
	for n := range g.nodeSnap {
		touchedNodes[n] = true
	}
	var nodes []*Node
	for n := range touchedNodes {
		if !n.Deleted {
			nodes = append(nodes, n)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for _, n := range nodes {
		if err := check("NODE", n.ID, n.Labels, n.Props); err != nil {
			return err
		}
	}
	touchedRels := map[*Rel]bool{}
	for r := range g.createdRels {
		touchedRels[r] = true
	}
	for r := range g.relSnap {
		touchedRels[r] = true
	}
	var rels []*Rel
	for r := range touchedRels {
		if !r.Deleted {
			rels = append(rels, r)
		}
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i].ID < rels[j].ID })
	for _, r := range rels {
		if err := check("RELATIONSHIP", r.ID, []string{r.Type}, r.Props); err != nil {
			return err
		}
	}
	return nil
}

func renderForError(v any) string {
	if s, ok := v.(string); ok {
		return "'" + s + "'"
	}
	return fmt.Sprint(v)
}

// validateExisting checks a new constraint against the stored data.
func (ex *exec) validateExisting(d schemaDef) error {
	seen := map[string]int64{}
	visit := func(id int64, props map[string]any) error {
		if err := checkOne(d, id, props); err != nil {
			return err
		}
		if d.Kind == "UNIQUENESS" || d.Kind == "KEY" {
			tuple, ok := tupleOf(d, props)
			if !ok {
				return nil
			}
			key := groupKey(tuple)
			if other, dup := seen[key]; dup {
				return constraintViolation("cannot create the constraint: %s and %s both have the same values for %s",
					describeEntity(d.Entity, other), describeEntity(d.Entity, id), strings.Join(d.Props, ", "))
			}
			seen[key] = id
		}
		return nil
	}
	if d.Entity == "NODE" {
		nodes, err := ex.g.scanNodes(d.Targets[0], nil)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if err := visit(n.ID, n.Props); err != nil {
				return err
			}
		}
		return nil
	}
	rels, err := ex.g.allRels()
	if err != nil {
		return err
	}
	for _, r := range rels {
		if r.Type == d.Targets[0] {
			if err := visit(r.ID, r.Props); err != nil {
				return err
			}
		}
	}
	return nil
}
