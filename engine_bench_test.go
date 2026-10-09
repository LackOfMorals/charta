package graphlite_test

import (
	"context"
	"fmt"
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
