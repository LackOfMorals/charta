package vector

import (
	"container/heap"
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
)

// Metric is the similarity function of an index.
type Metric int

const (
	// Cosine scores a pair as (1 + cos)/2, a value in [0, 1].
	Cosine Metric = iota
	// Euclidean scores a pair as 1/(1 + squared distance), a value in (0, 1].
	Euclidean
)

// ParseMetric accepts "cosine" or "euclidean" (any case).
func ParseMetric(s string) (Metric, bool) {
	switch s {
	case "cosine", "COSINE", "Cosine":
		return Cosine, true
	case "euclidean", "EUCLIDEAN", "Euclidean":
		return Euclidean, true
	}
	return 0, false
}

// Hit is one search result: the id a vector was stored under and its score
// (higher is more similar, as for vector.similarity.*).
type Hit struct {
	ID    int64
	Score float32
}

// Matrix holds the vectors of one index as a single contiguous float32 array so
// a search is a linear, cache-friendly scan with no per-vector decoding. Rows
// are stored normalised when the metric is cosine, which turns the similarity
// into a dot product.
//
// A Matrix is safe for concurrent use. Searches share a read lock; Upsert and
// Remove take the write lock briefly (a row is overwritten or appended, and a
// removed row is replaced by the last one), so a writer waits at most for the
// searches in flight.
type Matrix struct {
	dim    int
	metric Metric

	mu   sync.RWMutex
	ids  []int64
	data []float32
	pos  map[int64]int
}

// NewMatrix creates an empty matrix for vectors of the given dimension.
func NewMatrix(dim int, metric Metric) *Matrix {
	return &Matrix{dim: dim, metric: metric, pos: map[int64]int{}}
}

// Dim is the vector dimension.
func (m *Matrix) Dim() int { return m.dim }

// Len is the number of vectors held.
func (m *Matrix) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.ids)
}

// Bytes is the memory held by the vector data and ids.
func (m *Matrix) Bytes() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cap(m.data)*4 + cap(m.ids)*8 + len(m.pos)*24
}

// Upsert stores v under id, replacing any earlier vector. A vector of the wrong
// dimension is an error. For the cosine metric a zero vector has no direction,
// so it is removed from the matrix (it can never be a sensible result); ok
// reports whether the vector is now held.
func (m *Matrix) Upsert(id int64, v []float32) (ok bool, err error) {
	if len(v) != m.dim {
		return false, fmt.Errorf("vector has %d coordinates, the index has %d", len(v), m.dim)
	}
	row := append([]float32(nil), v...)
	if m.metric == Cosine && !Normalize32(row) {
		m.Remove(id)
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, exists := m.pos[id]; exists {
		copy(m.data[p*m.dim:(p+1)*m.dim], row)
		return true, nil
	}
	m.pos[id] = len(m.ids)
	m.ids = append(m.ids, id)
	m.data = append(m.data, row...)
	return true, nil
}

// Remove deletes the vector stored under id, if any.
func (m *Matrix) Remove(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, exists := m.pos[id]
	if !exists {
		return
	}
	last := len(m.ids) - 1
	if p != last {
		copy(m.data[p*m.dim:(p+1)*m.dim], m.data[last*m.dim:(last+1)*m.dim])
		m.ids[p] = m.ids[last]
		m.pos[m.ids[p]] = p
	}
	m.ids = m.ids[:last]
	m.data = m.data[:last*m.dim]
	delete(m.pos, id)
}

// minRowsPerWorker keeps a small matrix on one goroutine: below this (about
// 0.1 ms of scanning at dimension 384), starting
// goroutines costs more than it saves.
const minRowsPerWorker = 2048

// Search returns the k most similar vectors to query, best first; ties are
// broken by id so results are deterministic. The scan is split across
// goroutines for large matrices. It stops early with ctx.Err() if ctx is done.
func (m *Matrix) Search(ctx context.Context, query []float32, k int) ([]Hit, error) {
	if len(query) != m.dim {
		return nil, fmt.Errorf("query has %d coordinates, the index has %d", len(query), m.dim)
	}
	if k < 1 {
		return nil, fmt.Errorf("k must be at least 1")
	}
	q := query
	if m.metric == Cosine {
		q = append([]float32(nil), query...)
		if !Normalize32(q) {
			return nil, fmt.Errorf("cosine similarity is undefined for a zero query vector")
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	n := len(m.ids)
	workers := n / minRowsPerWorker
	if workers > runtime.GOMAXPROCS(0) {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers < 1 {
		workers = 1
	}
	chunk := (n + workers - 1) / workers
	results := make([][]Hit, workers)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for w := 0; w < workers; w++ {
		lo, hi := w*chunk, (w+1)*chunk
		if hi > n {
			hi = n
		}
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			h := &hitHeap{}
			for i := lo; i < hi; i++ {
				if i&1023 == 0 {
					if err := ctx.Err(); err != nil {
						errOnce.Do(func() { firstErr = err })
						return
					}
				}
				row := m.data[i*m.dim : (i+1)*m.dim]
				var score float32
				if m.metric == Cosine {
					score = (1 + Dot32(q, row)) / 2
				} else {
					score = 1 / (1 + SquaredL2(q, row))
				}
				h.offer(Hit{ID: m.ids[i], Score: score}, k)
			}
			results[w] = h.items
		}(w, lo, hi)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	merged := &hitHeap{}
	for _, r := range results {
		for _, hit := range r {
			merged.offer(hit, k)
		}
	}
	out := merged.items
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// hitHeap is a min-heap on (score, then larger id first) holding the best hits
// seen, so the root is the one to evict.
type hitHeap struct{ items []Hit }

func (h *hitHeap) Len() int { return len(h.items) }
func (h *hitHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.ID > b.ID
}
func (h *hitHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *hitHeap) Push(x any)    { h.items = append(h.items, x.(Hit)) }
func (h *hitHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}

// worse reports whether a ranks below b.
func worse(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.ID > b.ID
}

func (h *hitHeap) offer(x Hit, k int) {
	if len(h.items) < k {
		heap.Push(h, x)
		return
	}
	if worse(h.items[0], x) {
		h.items[0] = x
		heap.Fix(h, 0)
	}
}

// Builder assembles a Matrix from many vectors without the per-vector locking
// and copying of Upsert: rows are written straight into one array, normalised
// in parallel at the end, and the id index is built once.
type Builder struct {
	dim    int
	metric Metric
	ids    []int64
	data   []float32
}

// NewBuilder creates a Builder for vectors of the given dimension.
func NewBuilder(dim int, metric Metric) *Builder { return &Builder{dim: dim, metric: metric} }

// NextRow returns the slice to fill with the next vector's coordinates. Call
// Keep(id) if it holds a vector, or Discard() to drop it.
func (b *Builder) NextRow() []float32 {
	n := len(b.data)
	if cap(b.data)-n < b.dim {
		grown := make([]float32, n, max(2*cap(b.data), n+b.dim*1024))
		copy(grown, b.data)
		b.data = grown
	}
	b.data = b.data[:n+b.dim]
	return b.data[n:]
}

// Keep records the row returned by the last NextRow under id.
func (b *Builder) Keep(id int64) { b.ids = append(b.ids, id) }

// Discard drops the row returned by the last NextRow.
func (b *Builder) Discard() { b.data = b.data[:len(b.data)-b.dim] }

// Matrix finishes the build. Under cosine, rows are normalised (in parallel)
// and zero vectors, which have no direction, are left out.
func (b *Builder) Matrix() *Matrix {
	n := len(b.ids)
	if b.metric == Cosine {
		keep := make([]bool, n)
		workers := runtime.GOMAXPROCS(0)
		chunk := (n + workers - 1) / workers
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			lo, hi := w*chunk, min((w+1)*chunk, n)
			if lo >= hi {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := lo; i < hi; i++ {
					keep[i] = Normalize32(b.data[i*b.dim : (i+1)*b.dim])
				}
			}()
		}
		wg.Wait()
		out := 0
		for i := 0; i < n; i++ {
			if !keep[i] {
				continue
			}
			if out != i {
				copy(b.data[out*b.dim:(out+1)*b.dim], b.data[i*b.dim:(i+1)*b.dim])
				b.ids[out] = b.ids[i]
			}
			out++
		}
		b.ids, b.data = b.ids[:out], b.data[:out*b.dim]
	}
	m := &Matrix{dim: b.dim, metric: b.metric, ids: b.ids, data: b.data, pos: make(map[int64]int, len(b.ids))}
	for i, id := range m.ids {
		m.pos[id] = i
	}
	return m
}

// MergeBuilders concatenates the rows of several Builders (all for the same
// dimension and metric) into the first one, so rows can be collected by
// concurrent workers and combined at the end.
func MergeBuilders(bs ...*Builder) *Builder {
	if len(bs) == 0 {
		return nil
	}
	total, rows := 0, 0
	for _, b := range bs {
		total += len(b.data)
		rows += len(b.ids)
	}
	out := &Builder{dim: bs[0].dim, metric: bs[0].metric, ids: make([]int64, 0, rows), data: make([]float32, 0, total)}
	for _, b := range bs {
		out.ids = append(out.ids, b.ids...)
		out.data = append(out.data, b.data...)
	}
	return out
}
