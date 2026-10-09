package graphlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/LackOfMorals/graphlite/v2"
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
				db, err := graphlite.Open(":memory:")
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
				db, err := graphlite.Open(filepath.Join(b.TempDir(), "g.db"), graphlite.WithMaxReadConns(conns))
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
	db, err := graphlite.Open(filepath.Join(b.TempDir(), "g.db"))
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
