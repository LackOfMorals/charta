package vector

import (
	"context"
	"math/rand"
	"sort"
	"testing"
)

// reference ranks by brute force with the float64 definitions.
func reference(vecs map[int64][]float32, q []float32, k int, metric Metric) []Hit {
	var hits []Hit
	for id, v := range vecs {
		var score float32
		if metric == Cosine {
			c, ok := Cosine32(q, v)
			if !ok {
				continue
			}
			score = (1 + c) / 2
		} else {
			score = 1 / (1 + SquaredL2(q, v))
		}
		hits = append(hits, Hit{id, score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

func sameRanking(t *testing.T, got, want []Hit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d hits, want %d", len(got), len(want))
	}
	for i := range got {
		// Scores may differ in the last bits (normalised rows), so compare
		// ids only where the reference scores are clearly separated.
		if got[i].ID != want[i].ID {
			near := i+1 < len(want) && abs32(want[i].Score-want[i+1].Score) < 1e-5 ||
				i > 0 && abs32(want[i].Score-want[i-1].Score) < 1e-5
			if !near {
				t.Errorf("rank %d: got id %d (%.6f), want id %d (%.6f)", i, got[i].ID, got[i].Score, want[i].ID, want[i].Score)
			}
		}
		if abs32(got[i].Score-want[i].Score) > 1e-4 {
			t.Errorf("rank %d: score %.6f, want %.6f", i, got[i].Score, want[i].Score)
		}
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func TestSearchMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, metric := range []Metric{Cosine, Euclidean} {
		const dim = 24
		m := NewMatrix(dim, metric)
		vecs := map[int64][]float32{}
		for id := int64(1); id <= 2000; id++ {
			v := randVec(r, dim)
			vecs[id] = v
			if ok, err := m.Upsert(id, v); !ok || err != nil {
				t.Fatal(ok, err)
			}
		}
		q := randVec(r, dim)
		got, err := m.Search(context.Background(), q, 10)
		if err != nil {
			t.Fatal(err)
		}
		sameRanking(t, got, reference(vecs, q, 10, metric))
	}
}

func TestSearchOnALargeMatrixUsesTheParallelPath(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	const dim, n = 16, 3 * minRowsPerWorker
	m := NewMatrix(dim, Euclidean)
	vecs := map[int64][]float32{}
	for id := int64(1); id <= n; id++ {
		v := randVec(r, dim)
		vecs[id] = v
		m.Upsert(id, v)
	}
	q := randVec(r, dim)
	got, err := m.Search(context.Background(), q, 25)
	if err != nil {
		t.Fatal(err)
	}
	sameRanking(t, got, reference(vecs, q, 25, Euclidean))
}

func TestUpsertRemoveAndReplace(t *testing.T) {
	m := NewMatrix(2, Euclidean)
	m.Upsert(1, []float32{0, 0})
	m.Upsert(2, []float32{10, 10})
	m.Upsert(3, []float32{1, 1})
	hits, _ := m.Search(context.Background(), []float32{0, 0}, 3)
	if len(hits) != 3 || hits[0].ID != 1 || hits[1].ID != 3 || hits[2].ID != 2 {
		t.Fatalf("initial ranking %v", hits)
	}
	m.Remove(1) // the last row (3) moves into slot 0
	m.Upsert(2, []float32{0.5, 0.5})
	hits, _ = m.Search(context.Background(), []float32{0, 0}, 5)
	if len(hits) != 2 || hits[0].ID != 2 || hits[1].ID != 3 {
		t.Fatalf("after remove and replace %v", hits)
	}
	m.Remove(99) // unknown id is a no-op
	if m.Len() != 2 {
		t.Errorf("Len = %d", m.Len())
	}
}

func TestCosineIgnoresZeroVectorsAndLength(t *testing.T) {
	m := NewMatrix(2, Cosine)
	if ok, _ := m.Upsert(1, []float32{0, 0}); ok {
		t.Error("a zero vector has no cosine and must not be stored")
	}
	m.Upsert(2, []float32{5, 0})
	m.Upsert(3, []float32{0, 1})
	hits, err := m.Search(context.Background(), []float32{100, 0}, 2)
	if err != nil || hits[0].ID != 2 || hits[0].Score < 0.999 {
		t.Errorf("hits %v err %v", hits, err)
	}
	if _, err := m.Search(context.Background(), []float32{0, 0}, 1); err == nil {
		t.Error("a zero query has no cosine")
	}
}

func TestSearchErrorsAndCancellation(t *testing.T) {
	m := NewMatrix(2, Euclidean)
	if _, err := m.Upsert(1, []float32{1}); err == nil {
		t.Error("wrong dimension on upsert")
	}
	if _, err := m.Search(context.Background(), []float32{1, 2, 3}, 1); err == nil {
		t.Error("wrong query dimension")
	}
	if _, err := m.Search(context.Background(), []float32{1, 2}, 0); err == nil {
		t.Error("k < 1")
	}
	big := NewMatrix(4, Euclidean)
	for id := int64(0); id < 50000; id++ {
		big.Upsert(id, []float32{1, 2, 3, float32(id)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := big.Search(ctx, []float32{1, 2, 3, 4}, 5); err == nil {
		t.Error("a cancelled context must stop the scan")
	}
}

func TestConcurrentSearchAndUpdate(t *testing.T) {
	m := NewMatrix(8, Euclidean)
	r := rand.New(rand.NewSource(9))
	for id := int64(0); id < 1000; id++ {
		m.Upsert(id, randVec(r, 8))
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		rr := rand.New(rand.NewSource(10))
		for i := 0; i < 2000; i++ {
			id := int64(rr.Intn(1500))
			if i%3 == 0 {
				m.Remove(id)
			} else {
				m.Upsert(id, randVec(rr, 8))
			}
		}
	}()
	q := randVec(r, 8)
	for {
		select {
		case <-done:
			return
		default:
			if _, err := m.Search(context.Background(), q, 5); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func benchSearch(b *testing.B, n int, metric Metric) {
	r := rand.New(rand.NewSource(11))
	const dim = 384
	m := NewMatrix(dim, metric)
	for id := int64(0); id < int64(n); id++ {
		m.Upsert(id, randVec(r, dim))
	}
	q := randVec(r, dim)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Search(ctx, q, 10); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchCosine10k(b *testing.B)  { benchSearch(b, 10000, Cosine) }
func BenchmarkSearchCosine100k(b *testing.B) { benchSearch(b, 100000, Cosine) }
