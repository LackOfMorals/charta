package charta_test

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LackOfMorals/charta"
)

// BenchmarkQueries measures common query shapes on a 2,000-node chain graph.
func BenchmarkQueries(b *testing.B) {
	queries := []struct{ name, q string }{
		{"lookup", "MATCH (n:P {id: 1000}) RETURN n.name"},
		{"where_eq", "MATCH (n:P) WHERE n.id = 1000 RETURN n.name"},
		{"filter", "MATCH (n:P) WHERE n.grp = 3 RETURN n.id"},
		{"hop2", "MATCH (a:P {id: 10})-[:K]->()-[:K]->(c) RETURN c.id"},
		{"aggregate", "MATCH (n:P) RETURN n.grp, count(*) AS c"},
		{"create", "CREATE (:Tmp {x: 1})"},
	}
	{
		for _, qq := range queries {
			b.Run(qq.name, func(b *testing.B) {
				ctx := context.Background()
				db, err := charta.Open(":memory:")
				if err != nil {
					b.Fatal(err)
				}
				defer db.Close(ctx)
				for i := 0; i < 2000; i++ {
					if _, err := db.RunQuery(ctx, fmt.Sprintf("CREATE (:P {id: %d, name: 'n%d', grp: %d})", i, i, i%20), nil); err != nil {
						b.Fatal(err)
					}
				}
				if _, err := db.RunQuery(ctx, "MATCH (a:P), (b:P) WHERE b.id = a.id + 1 CREATE (a)-[:K]->(b)", nil); err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					res, err := db.RunQuery(ctx, qq.q, nil)
					if err != nil {
						b.Skip(err)
					}
					if _, err := res.Collect(ctx); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkParallelReads measures read throughput with concurrent callers on a
// file-backed database, at several read-pool sizes. Compare with -cpu 1,4.
func BenchmarkParallelReads(b *testing.B) {
	queries := []struct {
		name, q string
		param   bool
	}{
		{"lookup", "MATCH (n:P {id: $i}) RETURN n.v", true},
		{"scan", "MATCH (n:P) RETURN n.g AS g, count(*) AS c, sum(n.v) AS s", false},
	}
	for _, conns := range []int{1, 2, 4} {
		for _, qq := range queries {
			b.Run(fmt.Sprintf("conns=%d/%s", conns, qq.name), func(b *testing.B) {
				ctx := context.Background()
				db, err := charta.Open(filepath.Join(b.TempDir(), "g.db"), charta.WithMaxReadConns(conns))
				if err != nil {
					b.Fatal(err)
				}
				defer db.Close(ctx)
				if _, err := db.RunQuery(ctx, "UNWIND range(1, 20000) AS i CREATE (:P {id: i, v: i, g: i % 10})", nil); err != nil {
					b.Fatal(err)
				}
				if _, err := db.RunQuery(ctx, "CREATE INDEX FOR (n:P) ON (n.id)", nil); err != nil {
					b.Fatal(err)
				}
				var ctr atomic.Int64
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						var params map[string]any
						if qq.param {
							params = map[string]any{"i": ctr.Add(1)%20000 + 1}
						}
						res, err := db.RunQuery(ctx, qq.q, params)
						if err != nil {
							b.Error(err)
							return
						}
						if _, err := res.Collect(ctx); err != nil {
							b.Error(err)
							return
						}
					}
				})
			})
		}
	}
}

// BenchmarkFileWrites measures single-writer throughput on a file database,
// which must not regress when the read pool is added.
func BenchmarkFileWrites(b *testing.B) {
	ctx := context.Background()
	db, err := charta.Open(filepath.Join(b.TempDir(), "g.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close(ctx)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.RunQuery(ctx, "CREATE (:W {i: $i})", map[string]any{"i": int64(i)}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVectorSearch measures db.index.vector.queryNodes end to end at
// several sizes and dimension 384: the first (cold) query builds the in-memory
// matrix from the stored properties, later queries scan it. Sizes above 100k
// are skipped unless CHARTA_BIG=1 because loading them takes minutes.
func BenchmarkVectorSearch(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ctx := context.Background()
			db, err := charta.Open(filepath.Join(b.TempDir(), "g.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close(ctx)
			const dim = 384
			ddl := fmt.Sprintf("CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: %d}}", dim)
			if _, err := db.RunQuery(ctx, ddl, nil); err != nil {
				b.Fatal(err)
			}
			rnd := rand.New(rand.NewSource(1))
			vec := func() []any {
				v := make([]any, dim)
				for i := range v {
					v[i] = rnd.Float64()*2 - 1
				}
				return v
			}
			for lo := 0; lo < n; lo += 500 {
				rows := make([]any, 0, 500)
				for i := lo; i < lo+500 && i < n; i++ {
					rows = append(rows, map[string]any{"id": int64(i), "e": vec()})
				}
				if _, err := db.RunQuery(ctx, "UNWIND $rows AS r CREATE (:Doc {id: r.id, e: r.e})", map[string]any{"rows": rows}); err != nil {
					b.Fatal(err)
				}
			}
			q := vec()
			query := "CALL db.index.vector.queryNodes('v', 10, $q) YIELD node, score RETURN node.id, score"
			run := func() {
				res, err := db.RunQuery(ctx, query, map[string]any{"q": q})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := res.Collect(ctx); err != nil {
					b.Fatal(err)
				}
			}
			start := time.Now()
			run() // cold: builds the matrix
			b.Logf("cold first query (decode %d vectors into the matrix): %v", n, time.Since(start))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				run()
			}
		})
	}
}

// BenchmarkPropertyLookup compares an equality lookup with no index, a declared
// index and the automatic index on 100,000 nodes and 100,000 relationships.
func BenchmarkPropertyLookup(b *testing.B) {
	const n = 100000
	build := func(b *testing.B, opts ...charta.Option) *charta.DB {
		ctx := context.Background()
		db, err := charta.Open(filepath.Join(b.TempDir(), "g.db"), opts...)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { db.Close(ctx) })
		for lo := 0; lo < n; lo += 20000 {
			q := fmt.Sprintf("UNWIND range(%d, %d) AS i CREATE (a:P {id: i, name: 'p' + toString(i)}) CREATE (a)-[:R {w: i}]->(a)", lo, lo+19999)
			if _, err := db.RunQuery(ctx, q, nil); err != nil {
				b.Fatal(err)
			}
		}
		return db
	}
	lookup := func(b *testing.B, db *charta.DB, q string) {
		ctx := context.Background()
		var ctr atomic.Int64
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			res, err := db.RunQuery(ctx, q, map[string]any{"i": ctr.Add(7919) % n})
			if err != nil {
				b.Fatal(err)
			}
			if _, err := res.Collect(ctx); err != nil {
				b.Fatal(err)
			}
		}
	}
	const nodeQ = "MATCH (p:P {id: $i}) RETURN p.name"
	const relQ = "MATCH ()-[r:R {w: $i}]->() RETURN count(r)"
	b.Run("node/no_index", func(b *testing.B) {
		lookup(b, build(b, charta.WithoutAutomaticIndexes()), nodeQ)
	})
	b.Run("node/declared_index", func(b *testing.B) {
		db := build(b, charta.WithoutAutomaticIndexes())
		if err := db.CreatePropertyIndex(context.Background(), "P", "id"); err != nil {
			b.Fatal(err)
		}
		lookup(b, db, nodeQ)
	})
	b.Run("node/automatic_index", func(b *testing.B) {
		db := build(b)
		for i := 0; i < 5; i++ { // let the advisor notice the pattern
			res, _ := db.RunQuery(context.Background(), nodeQ, map[string]any{"i": int64(i)})
			res.Collect(context.Background())
		}
		time.Sleep(2 * time.Second) // the index is built in the background
		lookup(b, db, nodeQ)
	})
	b.Run("relationship/no_index", func(b *testing.B) {
		lookup(b, build(b, charta.WithoutAutomaticIndexes()), relQ)
	})
	b.Run("relationship/declared_index", func(b *testing.B) {
		db := build(b, charta.WithoutAutomaticIndexes())
		if _, err := db.RunQuery(context.Background(), "CREATE INDEX FOR ()-[r:R]-() ON (r.w)", nil); err != nil {
			b.Fatal(err)
		}
		lookup(b, db, relQ)
	})
}

// BenchmarkShortestPath measures shortest-path queries on a 100,000-node grid
// (316 x 316, every cell linked to its right and lower neighbour).
func BenchmarkShortestPath(b *testing.B) {
	const side = 316
	var sb strings.Builder
	sb.WriteString(`{"nodes":[`)
	for i := 0; i < side*side; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":"%d","labels":["G"],"props":{"k":%d}}`, i, i)
	}
	sb.WriteString(`],"edges":[`)
	first := true
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			for _, to := range []int{x + 1 + y*side, x + (y+1)*side} {
				if (to == x+1+y*side && x+1 >= side) || (to == x+(y+1)*side && y+1 >= side) {
					continue
				}
				if !first {
					sb.WriteByte(',')
				}
				first = false
				fmt.Fprintf(&sb, `{"type":"R","startId":"%d","endId":"%d","props":{}}`, x+y*side, to)
			}
		}
	}
	sb.WriteString(`]}`)
	ctx := context.Background()
	db, err := charta.Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close(ctx)
	if err := db.Import(ctx, strings.NewReader(sb.String()), charta.FormatJSON); err != nil {
		b.Fatal(err)
	}
	if _, err := db.RunQuery(ctx, "CREATE INDEX FOR (n:G) ON (n.k)", nil); err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name, q string
		params  map[string]any
	}{
		{"far-corner", "MATCH (a:G {k: $a}), (b:G {k: $b}), p = shortestPath((a)-[:R*]-(b)) RETURN length(p)", map[string]any{"a": 0, "b": side*side - 1}},
		{"near-directed", "MATCH (a:G {k: $a}), (b:G {k: $b}), p = shortestPath((a)-[:R*]->(b)) RETURN length(p)", map[string]any{"a": 0, "b": 3*side + 3}},
		{"near-all", "MATCH (a:G {k: $a}), (b:G {k: $b}), p = allShortestPaths((a)-[:R*]->(b)) RETURN count(p)", map[string]any{"a": 0, "b": 3*side + 3}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				res, err := db.RunQuery(ctx, c.q, c.params)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := res.Collect(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
