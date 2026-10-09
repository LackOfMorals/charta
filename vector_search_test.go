package charta_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"github.com/LackOfMorals/charta"
)

const vecIndexDDL = "CREATE VECTOR INDEX docs FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 3, `vector.similarity_function`: 'euclidean'}}"

func search(t testing.TB, ex interface {
	Run(context.Context, string, map[string]any) (*charta.Result, error)
}, q []float64, k int) []int64 {
	t.Helper()
	ctx := context.Background()
	res, err := ex.Run(ctx, "CALL db.index.vector.queryNodes('docs', $k, $q) YIELD node RETURN node.id AS id",
		map[string]any{"k": int64(k), "q": floatsToAny(q)})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := res.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, r := range recs {
		ids = append(ids, r.Values()[0].(int64))
	}
	return ids
}

type dbRunner struct{ db *charta.DB }

func (r dbRunner) Run(ctx context.Context, q string, p map[string]any) (*charta.Result, error) {
	return r.db.RunQuery(ctx, q, p)
}

func floatsToAny(f []float64) []any {
	out := make([]any, len(f))
	for i, x := range f {
		out[i] = x
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// bruteForce ranks the stored vectors by squared distance (ties by id).
func bruteForce(vecs map[int64][3]float64, q []float64, k int) []int64 {
	type hit struct {
		id int64
		d  float64
	}
	var hits []hit
	for id, v := range vecs {
		var d float64
		for i := range v {
			d += (v[i] - q[i]) * (v[i] - q[i])
		}
		hits = append(hits, hit{id, d})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].d != hits[j].d {
			return hits[i].d < hits[j].d
		}
		return hits[i].id < hits[j].id
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.id
	}
	return out
}

func TestVectorSearchStaysCorrectUnderChurn(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	if _, err := db.RunQuery(ctx, vecIndexDDL, nil); err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(3))
	vecs := map[int64][3]float64{}
	var next int64
	newVec := func() [3]float64 {
		// Integer coordinates keep float32 and float64 distances identical.
		return [3]float64{float64(r.Intn(40)), float64(r.Intn(40)), float64(r.Intn(40))}
	}
	for step := 0; step < 300; step++ {
		switch op := r.Intn(4); {
		case op <= 1 || len(vecs) < 5:
			next++
			v := newVec()
			if _, err := db.RunQuery(ctx, "CREATE (:Doc {id: $id, e: $e})", map[string]any{"id": next, "e": floatsToAny(v[:])}); err != nil {
				t.Fatal(err)
			}
			vecs[next] = v
		case op == 2:
			id := int64(r.Intn(int(next)) + 1)
			if _, ok := vecs[id]; !ok {
				continue
			}
			v := newVec()
			if _, err := db.RunQuery(ctx, "MATCH (d:Doc {id: $id}) SET d.e = $e", map[string]any{"id": id, "e": floatsToAny(v[:])}); err != nil {
				t.Fatal(err)
			}
			vecs[id] = v
		default:
			id := int64(r.Intn(int(next)) + 1)
			if _, ok := vecs[id]; !ok {
				continue
			}
			if _, err := db.RunQuery(ctx, "MATCH (d:Doc {id: $id}) DELETE d", map[string]any{"id": id}); err != nil {
				t.Fatal(err)
			}
			delete(vecs, id)
		}
		if step%5 == 0 {
			q := []float64{float64(r.Intn(40)), float64(r.Intn(40)), float64(r.Intn(40))}
			got, want := search(t, dbRunner{db}, q, 7), bruteForce(vecs, q, 7)
			// Equal distances can legitimately order differently between the
			// matrix and this reference; compare the distances, not the ids.
			if !sameDistances(vecs, q, got, want) {
				t.Fatalf("step %d: search %v, brute force %v", step, got, want)
			}
		}
	}
}

func sameDistances(vecs map[int64][3]float64, q []float64, a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	dist := func(id int64) float64 {
		v := vecs[id]
		var d float64
		for i := range v {
			d += (v[i] - q[i]) * (v[i] - q[i])
		}
		return d
	}
	for i := range a {
		if dist(a[i]) != dist(b[i]) {
			return false
		}
	}
	return true
}

func TestVectorSearchTransactionsCommitAndRollBack(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	for _, q := range []string{vecIndexDDL,
		"CREATE (:Doc {id: 1, e: [0.0, 0.0, 0.0]}), (:Doc {id: 2, e: [5.0, 5.0, 5.0]})"} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	origin := []float64{0, 0, 0}
	if got := search(t, dbRunner{db}, origin, 5); !sameIDs(got, []int64{1, 2}) { // builds the shared matrix
		t.Fatalf("initial %v", got)
	}

	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Run(ctx, "CREATE (:Doc {id: 3, e: [1.0, 0.0, 0.0]})", nil); err != nil {
		t.Fatal(err)
	}
	// Inside the transaction the search sees its own write.
	if got := search(t, tx, origin, 5); !sameIDs(got, []int64{1, 3, 2}) {
		t.Errorf("inside the transaction: %v", got)
	}
	// Outside it does not, until commit.
	if got := search(t, dbRunner{db}, origin, 5); !sameIDs(got, []int64{1, 2}) {
		t.Errorf("an uncommitted write leaked into the shared matrix: %v", got)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := search(t, dbRunner{db}, origin, 5); !sameIDs(got, []int64{1, 2}) {
		t.Errorf("after rollback: %v", got)
	}

	tx, err = db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Run(ctx, "CREATE (:Doc {id: 3, e: [1.0, 0.0, 0.0]})", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := search(t, dbRunner{db}, origin, 5); !sameIDs(got, []int64{1, 3, 2}) {
		t.Errorf("after commit: %v", got)
	}
}

func TestVectorSearchWhileWriting(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t, charta.WithBusyTimeout(5e9))
	if _, err := db.RunQuery(ctx, vecIndexDDL, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunQuery(ctx, "UNWIND range(1, 200) AS i CREATE (:Doc {id: i, e: [toFloat(i), 0.0, 0.0]})", nil); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res, err := db.RunQuery(ctx, "CALL db.index.vector.queryNodes('docs', 5, [0.0, 0.0, 0.0]) YIELD node, score RETURN node.id, score", nil)
				if err != nil {
					errs <- err
					return
				}
				recs, err := res.Collect(ctx)
				if err != nil {
					errs <- err
					return
				}
				for i := 1; i < len(recs); i++ {
					if recs[i].Values()[1].(float64) > recs[i-1].Values()[1].(float64) {
						errs <- fmt.Errorf("scores not descending: %v", recs)
						return
					}
				}
			}
		}()
	}
	for i := 201; i <= 400; i++ {
		if _, err := db.RunQuery(ctx, "CREATE (:Doc {id: $i, e: [toFloat($i), 0.0, 0.0]})", map[string]any{"i": int64(i)}); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 {
			if _, err := db.RunQuery(ctx, "MATCH (d:Doc {id: $i}) DELETE d", map[string]any{"i": int64(i - 5)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	if got := search(t, dbRunner{db}, []float64{0, 0, 0}, 3); !sameIDs(got, []int64{1, 2, 3}) {
		t.Errorf("final %v", got)
	}
}

func TestVectorSearchWithATinyCacheStillAnswers(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t, charta.WithMaxVectorCacheBytes(1))
	for _, q := range []string{vecIndexDDL, "UNWIND range(1, 50) AS i CREATE (:Doc {id: i, e: [toFloat(i), 0.0, 0.0]})"} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := search(t, dbRunner{db}, []float64{0, 0, 0}, 3); !sameIDs(got, []int64{1, 2, 3}) {
		t.Errorf("got %v", got)
	}
}

func TestVectorIndexRecreatedWithAnotherDimension(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	for _, q := range []string{vecIndexDDL, "CREATE (:Doc {id: 1, e: [0.0, 0.0, 0.0]})"} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	search(t, dbRunner{db}, []float64{0, 0, 0}, 1) // cache a 3-dimensional matrix
	for _, q := range []string{"DROP INDEX docs", "MATCH (d:Doc) SET d.e = [1.0, 2.0]",
		"CREATE VECTOR INDEX docs FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 2, `vector.similarity_function`: 'euclidean'}}"} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	res, err := db.RunQuery(ctx, "CALL db.index.vector.queryNodes('docs', 1, [1.0, 2.0]) YIELD node, score RETURN node.id, score", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := res.Single(ctx)
	if err != nil || rec.Values()[0] != int64(1) || rec.Values()[1] != 1.0 {
		t.Fatalf("got %v %v", rec, err)
	}
}

func TestVectorSearchOnARelationshipIndexIsUnsupported(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	if _, err := db.RunQuery(ctx, "CREATE VECTOR INDEX rv FOR ()-[r:R]-() ON (r.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}", nil); err != nil {
		t.Fatal(err)
	}
	_, err := db.RunQuery(ctx, "CALL db.index.vector.queryNodes('rv', 1, [1.0, 2.0]) YIELD node RETURN node", nil)
	var target *charta.ErrUnsupportedCypher
	if !errors.As(err, &target) {
		t.Errorf("got %v, want ErrUnsupportedCypher", err)
	}
}

func TestVectorSearchGoAPI(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	for _, q := range []string{vecIndexDDL, "CREATE (:Doc {id: 1, e: [0.0, 0.0, 0.0]}), (:Doc {id: 2, e: [3.0, 4.0, 0.0]})"} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.VectorSearch(ctx, "docs", []float64{0, 0, 0}, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
	if got[0].Node.Props["id"] != int64(1) || got[0].Score != 1 || got[1].Node.Props["id"] != int64(2) {
		t.Errorf("unexpected matches %+v %+v", got[0], got[1])
	}
	if got[1].Score < 0.0384 || got[1].Score > 0.0385 {
		t.Errorf("score %v, want about 1/26", got[1].Score)
	}
	if _, err := db.VectorSearch(ctx, "missing", []float64{0, 0, 0}, 1); err == nil {
		t.Error("an unknown index must be an error")
	}
	if _, err := db.VectorSearch(ctx, "docs", []float64{0, 0}, 1); err == nil {
		t.Error("a wrong dimension must be an error")
	}
	empty, err := db.VectorSearch(ctx, "docs", []float64{0, 0, 0}, 1)
	if err != nil || len(empty) != 1 {
		t.Errorf("k limit: %v %v", empty, err)
	}
}

// The matrix is built straight from the stored JSON, skipping the generic
// decoder. Whatever shape the data has, it must pick up exactly the valid vectors.
func TestVectorSearchBuildsFromAwkwardStoredData(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	for _, q := range []string{
		// Created before the index exists, so nothing has validated them.
		`CREATE (:Doc {id: 1, e: [0.0, 0.0, 0.0]}),
		        (:Doc {id: 2, e: [1, 0, 0]}),                              // integers
		        (:Doc {id: 3, e: vector([2, 0, 0], 3, FLOAT64)}),          // a stored VECTOR value
		        (:Doc {id: 4, e: [1.0e-5, -2.5e3, 0.5]}),                  // exponents
		        (:Doc {id: 5, e: [0.12345678901234568, 0.9, -0.1]}),       // 17 digits
		        (:Doc {id: 6, e: [1, 2]}),                                 // wrong dimension
		        (:Doc {id: 7, e: [1, 2, 3, 4]}),                           // wrong dimension
		        (:Doc {id: 8, e: 'not a vector'}),
		        (:Doc {id: 9, name: 'no vector at all'}),
		        (:Doc {id: 10, e: ['a', 'b', 'c'], tags: ['x]', '}']}),    // strings, brackets in other values
		        (:Other {id: 11, e: [0.0, 0.0, 0.0]})`,
		vecIndexDDL,
	} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := search(t, dbRunner{db}, []float64{0, 0, 0}, 20)
	// Ordered by distance from the origin: 0, 0.91, 1, 2, 2500.
	want := []int64{1, 5, 2, 3, 4}
	if !sameIDs(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
