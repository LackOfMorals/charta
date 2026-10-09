package interp

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
	"github.com/LackOfMorals/graphlite/v2/store"
)

func pathKey(p *Path) string {
	ids := make([]string, len(p.Rels))
	for i, r := range p.Rels {
		ids[i] = fmt.Sprint(r.ID)
	}
	return fmt.Sprintf("%d>%d:%s", p.Nodes[0].ID, p.Nodes[len(p.Nodes)-1].ID, strings.Join(ids, ","))
}

func pathKeys(ps []*Path) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = pathKey(p)
	}
	sort.Strings(out)
	return out
}

// randomGraph builds a small random multigraph (parallel relationships,
// self-loops, two relationship types) in a fresh in-memory database.
func randomGraph(t *testing.T, seed int64, nodes, rels int) *store.SQLiteStore {
	t.Helper()
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := rand.New(rand.NewSource(seed))
	runInternal(t, db, fmt.Sprintf("UNWIND range(1, %d) AS i CREATE (:N {i: i})", nodes), nil)
	for k := 0; k < rels; k++ {
		runInternal(t, db, fmt.Sprintf("MATCH (a:N {i: %d}), (b:N {i: %d}) CREATE (a)-[:%s]->(b)",
			r.Intn(nodes)+1, r.Intn(nodes)+1, []string{"R", "S"}[r.Intn(2)]), nil)
	}
	return db
}

func TestShortestPathStrategiesAgree(t *testing.T) {
	ctx := context.Background()
	for seed := int64(1); seed <= 6; seed++ {
		db := randomGraph(t, seed, 18, 36)
		g := newGraph(ctx, db.DB())
		ex := &exec{g: g}
		nodes, err := g.scanNodes("N", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range []syntax.Direction{syntax.DirRight, syntax.DirLeft, syntax.DirNone} {
			for _, typ := range []string{"", "R"} {
				for _, max := range []int64{-1, 3} {
					rp := &syntax.RelPattern{Dir: dir}
					if typ != "" {
						rp.Types = &syntax.LabelName{Name: typ}
					}
					for i := 0; i < 60; i++ {
						s, e := nodes[rand.Intn(len(nodes))], nodes[rand.Intn(len(nodes))]
						label := fmt.Sprintf("seed %d dir %d type %q max %d %d->%d", seed, dir, typ, max, s.ID, e.ID)
						used := map[int64]bool{}

						slowAll, err := ex.shortestPathsSlow(rp, s, e, 1, max, true, row{}, used)
						if err != nil {
							continue // the old algorithm's state cap: no oracle for this pair
						}
						bfsAll, err := ex.shortestPaths(rp, s, e, 1, max, true, row{}, used)
						if err != nil {
							t.Fatal(err)
						}
						if strings.Join(pathKeys(slowAll), ";") != strings.Join(pathKeys(bfsAll), ";") {
							t.Fatalf("%s: all shortest differ\n slow %v\n bfs  %v", label, pathKeys(slowAll), pathKeys(bfsAll))
						}

						slowOne, _ := ex.shortestPathsSlow(rp, s, e, 1, max, false, row{}, used)
						one, err := ex.shortestPaths(rp, s, e, 1, max, false, row{}, used) // bidirectional
						if err != nil {
							t.Fatal(err)
						}
						if len(slowOne) != len(one) {
							t.Fatalf("%s: one search found %d paths, the other %d", label, len(slowOne), len(one))
						}
						if len(one) == 1 {
							if len(one[0].Rels) != len(slowOne[0].Rels) {
								t.Fatalf("%s: lengths %d vs %d", label, len(one[0].Rels), len(slowOne[0].Rels))
							}
							// The path found must be a real path of that length, found among all shortest ones.
							found := false
							for _, k := range pathKeys(slowAll) {
								if k == pathKey(one[0]) {
									found = true
								}
							}
							if !found && s.ID != e.ID {
								t.Fatalf("%s: %s is not one of the shortest paths %v", label, pathKey(one[0]), pathKeys(slowAll))
							}
						}
					}
				}
			}
		}
	}
}

func TestBidirectionalSearchRespectsUsedRelationships(t *testing.T) {
	ctx := context.Background()
	db := randomGraph(t, 9, 12, 30)
	g := newGraph(ctx, db.DB())
	ex := &exec{g: g}
	nodes, _ := g.scanNodes("N", nil)
	rp := &syntax.RelPattern{Dir: syntax.DirNone}
	s, e := nodes[0], nodes[len(nodes)-1]
	free, _ := ex.shortestPaths(rp, s, e, 1, -1, false, row{}, map[int64]bool{})
	if len(free) == 0 {
		t.Skip("no path in this graph")
	}
	// Forbid every relationship of that path: the next path (if any) avoids them all.
	used := map[int64]bool{}
	for _, rel := range free[0].Rels {
		used[rel.ID] = true
	}
	for _, strategy := range []bool{false, true} {
		shortestBidirectionalDisabled = strategy
		got, err := ex.shortestPaths(rp, s, e, 1, -1, false, row{}, used)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range got {
			for _, rel := range p.Rels {
				if used[rel.ID] {
					t.Errorf("a relationship already used by the MATCH was reused (bidirectional disabled = %v)", strategy)
				}
			}
		}
	}
	shortestBidirectionalDisabled = false
}
