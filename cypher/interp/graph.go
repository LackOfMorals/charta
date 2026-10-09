package interp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// DB is the SQL surface the interpreter needs. *sql.DB, *sql.Tx and the
// store's executor types satisfy it.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Counters are the side effects of a statement.
type Counters struct {
	NodesCreated         int
	NodesDeleted         int
	RelationshipsCreated int
	RelationshipsDeleted int
	PropertiesSet        int
	PropertiesRemoved    int
	LabelsAdded          int
	LabelsRemoved        int
}

// graph is the per-statement view of the database. Nodes and relationships are
// cached by id so every reference shares one value; writes go to SQLite
// immediately, deletions are applied when the statement finishes (so a node
// whose relationships are deleted later in the same statement can still be
// deleted).
// entSnap is an existing entity's state before the statement changed it.
type entSnap struct {
	props  map[string]any
	labels []string
}

type graph struct {
	ctx context.Context
	db  DB

	nodes map[int64]*Node
	rels  map[int64]*Rel
	out   map[int64][]*Rel // outgoing relationships by node id, once loaded
	in    map[int64][]*Rel

	delNodes []*Node
	delRels  []*Rel
	detach   map[int64]bool // nodes deleted with DETACH DELETE

	// Net property/label counters are computed when the statement finishes:
	// created entities contribute their final state, existing entities the
	// difference from a snapshot taken before their first change.
	createdNodes map[*Node]bool
	createdRels  map[*Rel]bool
	nodeSnap     map[*Node]entSnap
	relSnap      map[*Rel]entSnap

	counters Counters
}

func newGraph(ctx context.Context, db DB) *graph {
	return &graph{
		ctx: ctx, db: db,
		nodes: map[int64]*Node{}, rels: map[int64]*Rel{},
		out: map[int64][]*Rel{}, in: map[int64][]*Rel{},
		detach:       map[int64]bool{},
		createdNodes: map[*Node]bool{}, createdRels: map[*Rel]bool{},
		nodeSnap: map[*Node]entSnap{}, relSnap: map[*Rel]entSnap{},
	}
}

// ─── property encoding ───────────────────────────────────────────────────────

// checkPropValue verifies that v can be stored as a property: a boolean,
// number or string, or a list of one such type.
func checkPropValue(key string, v any) error {
	switch x := v.(type) {
	case nil, bool, int64, string:
		return nil
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil
		}
		return nil
	case []any:
		var first any
		for i, e := range x {
			switch e.(type) {
			case bool, int64, float64, string:
			default:
				return errorf("TypeError", "InvalidPropertyType", "property `%s`: collections containing %s cannot be stored as properties", key, typeName(e))
			}
			if i == 0 {
				first = e
			} else if typeName(first) != typeName(e) && !(isNumber(first) && isNumber(e)) {
				return errorf("TypeError", "InvalidPropertyType", "property `%s`: a collection property must hold values of one type", key)
			}
		}
		return nil
	}
	return errorf("TypeError", "InvalidPropertyType", "property `%s`: %s values cannot be stored as properties", key, typeName(v))
}

func encodeProps(props map[string]any) (string, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteByte(':')
		if err := encodeValue(&sb, props[k]); err != nil {
			return "", err
		}
	}
	sb.WriteByte('}')
	return sb.String(), nil
}

// encodeValue writes v as JSON, keeping floats distinguishable from integers
// (an integral float is written with ".0").
func encodeValue(sb *strings.Builder, v any) error {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		sb.WriteString(strconv.FormatBool(x))
	case int64:
		sb.WriteString(strconv.FormatInt(x, 10))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errorf("TypeError", "InvalidPropertyType", "NaN and infinity cannot be stored as properties")
		}
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		sb.WriteString(s)
	case string:
		b, _ := json.Marshal(x)
		sb.Write(b)
	case []any:
		sb.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			if err := encodeValue(sb, e); err != nil {
				return err
			}
		}
		sb.WriteByte(']')
	default:
		return errorf("TypeError", "InvalidPropertyType", "%s values cannot be stored as properties", typeName(v))
	}
	return nil
}

func decodeProps(s string) (map[string]any, error) {
	props := map[string]any{}
	if s == "" || s == "{}" {
		return props, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode properties %q: %w", s, err)
	}
	for k, v := range raw {
		props[k] = fromJSON(v)
	}
	return props, nil
}

func fromJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if !strings.ContainsAny(s, ".eE") {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i
			}
		}
		f, _ := strconv.ParseFloat(s, 64)
		return f
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fromJSON(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = fromJSON(e)
		}
		return out
	}
	return v
}

func splitLabels(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// ─── reads ───────────────────────────────────────────────────────────────────

func (g *graph) internNode(id int64, labels, props string) (*Node, error) {
	if n, ok := g.nodes[id]; ok {
		return n, nil
	}
	p, err := decodeProps(props)
	if err != nil {
		return nil, err
	}
	n := &Node{ID: id, Labels: splitLabels(labels), Props: p}
	g.nodes[id] = n
	return n, nil
}

func (g *graph) internRel(id int64, typ string, start, end int64, props string) (*Rel, error) {
	if r, ok := g.rels[id]; ok {
		return r, nil
	}
	p, err := decodeProps(props)
	if err != nil {
		return nil, err
	}
	r := &Rel{ID: id, Type: typ, Start: start, End: end, Props: p}
	g.rels[id] = r
	return r, nil
}

// scanNodes returns every live node, restricted to those with the given label
// when it is non-empty, in id order.
//
// hints are property equalities the caller will re-check in Go; they only let
// SQLite skip nodes that cannot match, so each must hold for every true match.
func (g *graph) scanNodes(label string, hints []propHint) ([]*Node, error) {
	var sb strings.Builder
	var args []any
	sb.WriteString(`SELECT n.id, n.labels, n.props FROM nodes n WHERE 1`)
	if label != "" {
		sb.WriteString(` AND EXISTS (SELECT 1 FROM node_labels l WHERE l.node_id = n.id AND l.label = ?)`)
		args = append(args, label)
	}
	for _, h := range hints {
		sb.WriteString(` AND json_extract(n.props, ?) = ?`)
		args = append(args, `$."`+h.key+`"`, h.val)
	}
	sb.WriteString(` ORDER BY n.id`)
	rows, err := g.db.QueryContext(g.ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	type rec struct {
		id            int64
		labels, props string
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.labels, &r.props); err != nil {
			rows.Close()
			return nil, err
		}
		recs = append(recs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]*Node, 0, len(recs))
	for _, r := range recs {
		n, err := g.internNode(r.id, r.labels, r.props)
		if err != nil {
			return nil, err
		}
		if !n.Deleted {
			out = append(out, n)
		}
	}
	return out, nil
}

func (g *graph) node(id int64) (*Node, error) {
	if n, ok := g.nodes[id]; ok {
		return n, nil
	}
	var labels, props string
	err := g.queryRow(`SELECT labels, props FROM nodes WHERE id = ?`, []any{id}, &labels, &props)
	if err != nil {
		return nil, err
	}
	return g.internNode(id, labels, props)
}

func (g *graph) queryRow(q string, args []any, dest ...any) error {
	rows, err := g.db.QueryContext(g.ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

// loadAdj loads the relationships on one side of a node.
func (g *graph) loadAdj(id int64, outgoing bool) ([]*Rel, error) {
	cache := g.in
	col := "end_id"
	if outgoing {
		cache, col = g.out, "start_id"
	}
	if rs, ok := cache[id]; ok {
		return rs, nil
	}
	rows, err := g.db.QueryContext(g.ctx,
		`SELECT id, type, start_id, end_id, props FROM edges WHERE `+col+` = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		recs = append(recs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rs := make([]*Rel, 0, len(recs))
	for _, r := range recs {
		rel, err := g.internRel(r.id, r.typ, r.start, r.end, r.props)
		if err != nil {
			return nil, err
		}
		rs = append(rs, rel)
	}
	cache[id] = rs
	return rs, nil
}

// outRels and inRels return the live relationships leaving and entering a node.
func (g *graph) outRels(id int64) ([]*Rel, error) { return g.liveRels(id, true) }
func (g *graph) inRels(id int64) ([]*Rel, error)  { return g.liveRels(id, false) }

func (g *graph) liveRels(id int64, outgoing bool) ([]*Rel, error) {
	rs, err := g.loadAdj(id, outgoing)
	if err != nil {
		return nil, err
	}
	live := rs[:0:0]
	for _, r := range rs {
		if !r.Deleted {
			live = append(live, r)
		}
	}
	return live, nil
}

// allRels returns every live relationship in id order.
func (g *graph) allRels() ([]*Rel, error) {
	rows, err := g.db.QueryContext(g.ctx, `SELECT id, type, start_id, end_id, props FROM edges ORDER BY id`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		recs = append(recs, r)
	}
	rows.Close()
	var out []*Rel
	for _, r := range recs {
		rel, err := g.internRel(r.id, r.typ, r.start, r.end, r.props)
		if err != nil {
			return nil, err
		}
		if !rel.Deleted {
			out = append(out, rel)
		}
	}
	return out, nil
}

// ─── writes ──────────────────────────────────────────────────────────────────

func cleanProps(props map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(props))
	for k, v := range props {
		if v == nil {
			continue // a null property is no property
		}
		if err := checkPropValue(k, v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func (g *graph) createNode(labels []string, props map[string]any) (*Node, error) {
	props, err := cleanProps(props)
	if err != nil {
		return nil, err
	}
	enc, err := encodeProps(props)
	if err != nil {
		return nil, err
	}
	labels = dedupe(labels)
	res, err := g.db.ExecContext(g.ctx, `INSERT INTO nodes (labels, props) VALUES (?, ?)`, strings.Join(labels, ","), enc)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	n := &Node{ID: id, Labels: labels, Props: props}
	g.nodes[id] = n
	g.createdNodes[n] = true
	g.counters.NodesCreated++
	return n, nil
}

func (g *graph) createRel(typ string, start, end *Node, props map[string]any) (*Rel, error) {
	props, err := cleanProps(props)
	if err != nil {
		return nil, err
	}
	enc, err := encodeProps(props)
	if err != nil {
		return nil, err
	}
	res, err := g.db.ExecContext(g.ctx, `INSERT INTO edges (type, start_id, end_id, props) VALUES (?, ?, ?, ?)`, typ, start.ID, end.ID, enc)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	r := &Rel{ID: id, Type: typ, Start: start.ID, End: end.ID, Props: props}
	g.rels[id] = r
	// Keep any loaded adjacency lists current.
	if rs, ok := g.out[start.ID]; ok {
		g.out[start.ID] = append(rs, r)
	}
	if rs, ok := g.in[end.ID]; ok {
		g.in[end.ID] = append(rs, r)
	}
	g.createdRels[r] = true
	g.counters.RelationshipsCreated++
	return r, nil
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// setProps replaces an entity's property map and persists it. set and removed
// are the numbers of properties assigned and removed, for the counters.
func (g *graph) persistNodeProps(n *Node) error {
	enc, err := encodeProps(n.Props)
	if err != nil {
		return err
	}
	_, err = g.db.ExecContext(g.ctx, `UPDATE nodes SET props = ? WHERE id = ?`, enc, n.ID)
	return err
}

func (g *graph) persistRelProps(r *Rel) error {
	enc, err := encodeProps(r.Props)
	if err != nil {
		return err
	}
	_, err = g.db.ExecContext(g.ctx, `UPDATE edges SET props = ? WHERE id = ?`, enc, r.ID)
	return err
}

func (g *graph) persistLabels(n *Node) error {
	_, err := g.db.ExecContext(g.ctx, `UPDATE nodes SET labels = ? WHERE id = ?`, strings.Join(n.Labels, ","), n.ID)
	return err
}

// setNodeProp sets (or, for nil, removes) one property.
func (g *graph) setNodeProp(n *Node, key string, v any) error {
	if n.Deleted {
		return deletedAccess()
	}
	if v == nil {
		if _, had := n.Props[key]; had {
			g.snapNode(n)
			delete(n.Props, key)
			return g.persistNodeProps(n)
		}
		return nil
	}
	if err := checkPropValue(key, v); err != nil {
		return err
	}
	g.snapNode(n)
	n.Props[key] = v
	return g.persistNodeProps(n)
}

func (g *graph) setRelProp(r *Rel, key string, v any) error {
	if r.Deleted {
		return deletedAccess()
	}
	if v == nil {
		if _, had := r.Props[key]; had {
			g.snapRel(r)
			delete(r.Props, key)
			return g.persistRelProps(r)
		}
		return nil
	}
	if err := checkPropValue(key, v); err != nil {
		return err
	}
	g.snapRel(r)
	r.Props[key] = v
	return g.persistRelProps(r)
}

func (g *graph) addLabels(n *Node, labels []string) error {
	if n.Deleted {
		return deletedAccess()
	}
	changed := false
	for _, l := range labels {
		if !n.hasLabel(l) {
			g.snapNode(n)
			n.Labels = append(n.Labels, l)
			changed = true
		}
	}
	if changed {
		return g.persistLabels(n)
	}
	return nil
}

func (g *graph) removeLabels(n *Node, labels []string) error {
	if n.Deleted {
		return deletedAccess()
	}
	changed := false
	for _, l := range labels {
		for i, x := range n.Labels {
			if x == l {
				g.snapNode(n)
				n.Labels = append(append([]string{}, n.Labels[:i]...), n.Labels[i+1:]...)
				changed = true
				break
			}
		}
	}
	if changed {
		return g.persistLabels(n)
	}
	return nil
}

func (g *graph) snapNode(n *Node) {
	if g.createdNodes[n] {
		return
	}
	if _, ok := g.nodeSnap[n]; !ok {
		g.nodeSnap[n] = entSnap{props: copyMap(n.Props), labels: append([]string(nil), n.Labels...)}
	}
}

func (g *graph) snapRel(r *Rel) {
	if g.createdRels[r] {
		return
	}
	if _, ok := g.relSnap[r]; !ok {
		g.relSnap[r] = entSnap{props: copyMap(r.Props)}
	}
}

// diffProps counts the properties added and removed between two states; a
// changed value counts as one of each.
func diffProps(before, after map[string]any) (added, removed int) {
	for k, av := range after {
		bv, had := before[k]
		switch {
		case !had:
			added++
		case groupKey(bv) != groupKey(av):
			added++
			removed++
		}
	}
	for k := range before {
		if _, still := after[k]; !still {
			removed++
		}
	}
	return
}

// netCounters fills in the property and label counters from the final state.
func (g *graph) netCounters() {
	for n := range g.createdNodes {
		if n.Deleted {
			continue
		}
		g.counters.PropertiesSet += len(n.Props)
		g.counters.LabelsAdded += len(n.Labels)
	}
	for r := range g.createdRels {
		if !r.Deleted {
			g.counters.PropertiesSet += len(r.Props)
		}
	}
	for n, s := range g.nodeSnap {
		if n.Deleted {
			continue
		}
		a, r := diffProps(s.props, n.Props)
		g.counters.PropertiesSet += a
		g.counters.PropertiesRemoved += r
		for _, l := range n.Labels {
			if !containsStr(s.labels, l) {
				g.counters.LabelsAdded++
			}
		}
		for _, l := range s.labels {
			if !n.hasLabel(l) {
				g.counters.LabelsRemoved++
			}
		}
	}
	for r, s := range g.relSnap {
		if r.Deleted {
			continue
		}
		a, rm := diffProps(s.props, r.Props)
		g.counters.PropertiesSet += a
		g.counters.PropertiesRemoved += rm
	}
}

func deletedAccess() error {
	return errorf("EntityNotFound", "DeletedEntityAccess", "the entity has been deleted in this statement")
}

// deleteRel marks a relationship deleted; it is removed when the statement ends.
func (g *graph) deleteRel(r *Rel) {
	if r.Deleted {
		return
	}
	r.Deleted = true
	g.delRels = append(g.delRels, r)
}

// deleteNode marks a node deleted. With detach, its relationships are deleted
// with it.
func (g *graph) deleteNode(n *Node, detach bool) error {
	if n.Deleted {
		return nil
	}
	if detach {
		out, err := g.outRels(n.ID)
		if err != nil {
			return err
		}
		in, err := g.inRels(n.ID)
		if err != nil {
			return err
		}
		for _, r := range append(out, in...) {
			g.deleteRel(r)
		}
	}
	n.Deleted = true
	g.delNodes = append(g.delNodes, n)
	return nil
}

// finish applies the deletions. A node that still has a live relationship at
// this point cannot be deleted.
func (g *graph) finish() error {
	for _, n := range g.delNodes {
		out, err := g.outRels(n.ID)
		if err != nil {
			return err
		}
		in, err := g.inRels(n.ID)
		if err != nil {
			return err
		}
		if len(out)+len(in) > 0 {
			return errorf("ConstraintVerificationFailed", "DeleteConnectedNode", "cannot delete node<%d>, because it still has relationships", n.ID)
		}
	}
	for _, r := range g.delRels {
		if _, err := g.db.ExecContext(g.ctx, `DELETE FROM edges WHERE id = ?`, r.ID); err != nil {
			return err
		}
		g.counters.RelationshipsDeleted++
		g.counters.PropertiesRemoved += 0
	}
	for _, n := range g.delNodes {
		if _, err := g.db.ExecContext(g.ctx, `DELETE FROM nodes WHERE id = ?`, n.ID); err != nil {
			return err
		}
		g.counters.NodesDeleted++
	}
	g.netCounters()
	return nil
}
