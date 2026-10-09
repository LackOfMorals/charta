package interp

import (
	"context"
	"database/sql"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/LackOfMorals/charta/cypher/vector"
)

// Vector search keeps one in-memory matrix per vector index (see
// vector.Matrix) so a query scans contiguous float32 rows instead of decoding
// JSON: decoding a 384-float property costs about 10 us, the arithmetic on it
// about 0.1 us.
//
// A matrix is built lazily from the database on first use and then kept current
// from the entities each committed statement created, changed or deleted
// (VectorDelta). Only statements that cannot have uncommitted writes of their
// own use the shared matrix; any other statement builds a private one from the
// rows it can see, which is slower but always right.

// VectorDelta says how one node's vector changed in a statement: Vec is the new
// vector, or nil if the node is no longer in the index.
type VectorDelta struct {
	Index int64 // schema definition id
	Node  int64
	Vec   []float32
}

// vecCache is the shared state, guarded by Engine.vecMu.
type vecCache struct {
	matrices map[int64]*vector.Matrix
	epoch    uint64 // bumped by every applied delta batch and schema change
}

// DefaultVectorCacheBytes bounds the memory the matrices may use in total.
const DefaultVectorCacheBytes = 512 << 20

// ApplyVectorDeltas updates the cached matrices after a statement committed.
// Call it only after the commit succeeded.
func (e *Engine) ApplyVectorDeltas(ds []VectorDelta) {
	if e == nil || len(ds) == 0 {
		return
	}
	e.vecMu.Lock()
	defer e.vecMu.Unlock()
	e.vec.epoch++
	for _, d := range ds {
		m := e.vec.matrices[d.Index]
		if m == nil {
			continue
		}
		if d.Vec == nil {
			m.Remove(d.Node)
		} else {
			_, _ = m.Upsert(d.Node, d.Vec)
		}
	}
}

// dropVectorMatrix forgets a cached matrix (the index was dropped or recreated).
func (e *Engine) dropVectorMatrix(id int64) {
	if e == nil {
		return
	}
	e.vecMu.Lock()
	defer e.vecMu.Unlock()
	delete(e.vec.matrices, id)
	e.vec.epoch++
}

func (e *Engine) vectorCacheBytes() int64 {
	if e.VectorCacheBytes > 0 {
		return e.VectorCacheBytes
	}
	return DefaultVectorCacheBytes
}

// float32s converts a stored vector property to float32 coordinates.
func float32s(v any, dims int) ([]float32, bool) {
	switch x := v.(type) {
	case vector.Vector:
		if x.Dimension() != dims {
			return nil, false
		}
		out := make([]float32, dims)
		for i := range out {
			out[i] = float32(x.Float(i))
		}
		return out, true
	case []any:
		if len(x) != dims {
			return nil, false
		}
		out := make([]float32, dims)
		for i, e := range x {
			switch n := e.(type) {
			case int64:
				out[i] = float32(n)
			case float64:
				if math.IsNaN(n) || math.IsInf(n, 0) {
					return nil, false
				}
				out[i] = float32(n)
			default:
				return nil, false
			}
		}
		return out, true
	}
	return nil, false
}

func metricOf(d schemaDef) vector.Metric {
	if d.VectorSim == "euclidean" {
		return vector.Euclidean
	}
	return vector.Cosine
}

// buildMatrix reads every vector of the index from the database snapshot the
// statement sees. Zero vectors cannot be scored under cosine and are skipped.
func (g *graph) buildMatrix(d schemaDef) (*vector.Matrix, error) {
	if d.Entity == "NODE" && pushableKey(d.Props[0]) {
		return g.buildMatrixFast(d)
	}
	m := vector.NewMatrix(d.VectorDims, metricOf(d))
	nodes, err := g.scanNodes(d.Targets[0], nil)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if v, ok := float32s(n.Props[d.Props[0]], d.VectorDims); ok {
			if _, err := m.Upsert(n.ID, v); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// buildMatrixFast reads the vectors without decoding each node's properties:
// it fetches the raw JSON text and pulls out just the vector property
// (vector.StoredVector), parsing the numbers straight into matrix rows. The
// generic decoder cost about 30 us per 384-dimensional vector (a Node, a map and
// a boxed []any of float64 each) and SQLite's own json_extract about the same,
// since it parses the whole document. Fetching a row takes about 4 us, parsing a
// 17-digit embedding about 10, so the rows are handed to a pool of parser
// goroutines and the build runs at the speed of the fetch.
func (g *graph) buildMatrixFast(d schemaDef) (*vector.Matrix, error) {
	rows, err := g.db.QueryContext(g.ctx,
		`SELECT n.id, n.props FROM nodes n
		 WHERE EXISTS (SELECT 1 FROM node_labels l WHERE l.node_id = n.id AND l.label = ?)`, d.Targets[0])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prop := d.Props[0]

	workers := min(runtime.GOMAXPROCS(0), 8)
	batches := make(chan *rowBatch, 2*workers)
	free := make(chan *rowBatch, 2*workers+1)
	builders := make([]*vector.Builder, workers)
	var wg sync.WaitGroup
	for w := range builders {
		builders[w] = vector.NewBuilder(d.VectorDims, metricOf(d))
		wg.Add(1)
		go func(b *vector.Builder) {
			defer wg.Done()
			for batch := range batches {
				for i, id := range batch.ids {
					row := b.NextRow()
					if vector.StoredVector(unsafe.String(&batch.data[batch.offs[i]], batch.offs[i+1]-batch.offs[i]), prop, row) {
						b.Keep(id)
					} else {
						b.Discard()
					}
				}
				batch.reset()
				select {
				case free <- batch:
				default:
				}
			}
		}(builders[w])
	}

	next := func() *rowBatch {
		select {
		case b := <-free:
			return b
		default:
			return &rowBatch{}
		}
	}
	cur := next()
	var scanErr error
	for rows.Next() {
		var id int64
		var raw sql.RawBytes
		if scanErr = rows.Scan(&id, &raw); scanErr != nil {
			break
		}
		cur.add(id, raw) // copies raw: it is only valid until the next row
		if len(cur.ids) >= 64 || len(cur.data) >= 1<<20 {
			if g.ctx.Err() != nil {
				scanErr = g.ctx.Err()
				break
			}
			batches <- cur
			cur = next()
		}
	}
	if scanErr == nil {
		scanErr = rows.Err()
	}
	if scanErr == nil && len(cur.ids) > 0 {
		batches <- cur
	}
	close(batches)
	wg.Wait()
	if scanErr != nil {
		return nil, scanErr
	}
	return vector.MergeBuilders(builders...).Matrix(), nil
}

// rowBatch is a run of fetched rows: ids and the raw property text of each, laid
// out back to back in data (row i is data[offs[i]:offs[i+1]]).
type rowBatch struct {
	ids  []int64
	offs []int
	data []byte
}

func (b *rowBatch) add(id int64, props []byte) {
	if len(b.offs) == 0 {
		b.offs = append(b.offs, 0)
	}
	b.ids = append(b.ids, id)
	b.data = append(b.data, props...)
	b.offs = append(b.offs, len(b.data))
}

func (b *rowBatch) reset() {
	b.ids, b.offs, b.data = b.ids[:0], b.offs[:0], b.data[:0]
}

// matrixFor returns the matrix to search: the shared one when the statement may
// use it, otherwise a private one built from what the statement can see.
func (g *graph) matrixFor(d schemaDef) (*vector.Matrix, error) {
	e := g.eng
	if e == nil || !g.vectorCache {
		return g.buildMatrix(d)
	}
	e.vecMu.Lock()
	if m := e.vec.matrices[d.ID]; m != nil {
		e.vecMu.Unlock()
		return m, nil
	}
	if e.vec.matrices == nil {
		e.vec.matrices = map[int64]*vector.Matrix{}
	}
	epoch := e.vec.epoch
	e.vecMu.Unlock()

	// Build outside the lock (it can take a while), then install it only if no
	// commit or schema change happened meanwhile; otherwise the matrix may have
	// missed an update, so use it for this query but do not keep it.
	m, err := g.buildMatrix(d)
	if err != nil {
		return nil, err
	}
	e.vecMu.Lock()
	defer e.vecMu.Unlock()
	if e.vec.epoch != epoch {
		return m, nil
	}
	var used int64
	for _, other := range e.vec.matrices {
		used += int64(other.Bytes())
	}
	if used+int64(m.Bytes()) > e.vectorCacheBytes() {
		return m, nil // over the memory budget: answer from a private matrix
	}
	e.vec.matrices[d.ID] = m
	return m, nil
}

// vectorDeltas works out how the statement's changes affect the vector indexes.
// It is called from finish() once the statement's own checks have passed.
func (g *graph) vectorDeltas(idx []schemaDef) {
	for _, d := range idx {
		if d.Entity != "NODE" {
			continue
		}
		label, prop := d.Targets[0], d.Props[0]
		seen := map[*Node]bool{}
		consider := func(n *Node) {
			if seen[n] {
				return
			}
			seen[n] = true
			delta := VectorDelta{Index: d.ID, Node: n.ID}
			if !n.Deleted && n.hasLabel(label) {
				if v, ok := float32s(n.Props[prop], d.VectorDims); ok {
					delta.Vec = v
				}
			}
			g.deltas = append(g.deltas, delta)
		}
		for n := range g.createdNodes {
			consider(n)
		}
		for n := range g.nodeSnap {
			consider(n)
		}
		for _, n := range g.delNodes {
			consider(n)
		}
	}
}

// ─── db.index.vector.queryNodes ──────────────────────────────────────────────

func (ex *exec) queryVectorNodes(ctx context.Context, args []any) ([]map[string]any, error) {
	name, _ := args[0].(string)
	if args[0] == nil || args[1] == nil || args[2] == nil {
		return nil, nil
	}
	k, ok := args[1].(int64)
	if !ok || k < 1 {
		return nil, argErr("db.index.vector.queryNodes: the number of neighbours must be a positive integer, got %v", args[1])
	}
	defs, err := ex.g.loadSchema()
	if err != nil {
		return nil, err
	}
	var def *schemaDef
	for i := range defs {
		if defs[i].Name == name && !defs[i].Constraint && defs[i].Kind == "VECTOR" {
			def = &defs[i]
		}
	}
	if def == nil {
		return nil, errorf("ProcedureError", "IndexNotFound", "there is no vector index named `%s`", name)
	}
	if def.Entity != "NODE" {
		return nil, unsupported("db.index.vector.queryNodes on a relationship vector index")
	}
	q, ok := float32s(args[2], def.VectorDims)
	if !ok {
		if l, isList := args[2].([]any); isList && len(l) != def.VectorDims {
			return nil, argErr("the query vector has %d coordinates, but index `%s` has %d", len(l), name, def.VectorDims)
		}
		if v, isVec := args[2].(vector.Vector); isVec && v.Dimension() != def.VectorDims {
			return nil, argErr("the query vector has %d coordinates, but index `%s` has %d", v.Dimension(), name, def.VectorDims)
		}
		return nil, typeErr("the query vector must be a list of numbers or a VECTOR, got %s", typeName(args[2]))
	}
	m, err := ex.g.matrixFor(*def)
	if err != nil {
		return nil, err
	}
	// Ask for more than k: a hit can be a node this statement's snapshot does
	// not contain (committed after it began), which is skipped.
	hits, err := m.Search(ctx, q, int(k)+8)
	if err != nil {
		return nil, argErr("%v", err)
	}
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	if err := ex.g.preloadNodes(ids); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, h := range hits {
		n, err := ex.g.node(h.ID)
		if err != nil || n == nil || n.Deleted || !n.hasLabel(def.Targets[0]) {
			continue
		}
		out = append(out, map[string]any{"node": n, "score": float64(h.Score)})
		if int64(len(out)) == k {
			break
		}
	}
	return out, nil
}
