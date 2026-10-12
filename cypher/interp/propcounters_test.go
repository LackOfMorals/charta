package interp_test

import (
	"fmt"
	"strings"
	"testing"
)

// Property counters are net: they compare each entity before and after the
// statement, so a key set twice counts once, a key put back to its original
// value counts not at all, and deleting an entity removes the properties it
// had when the statement began. These cases pin that behaviour for the
// journal that records the state before each change.
func TestPropertyCountersAreNet(t *testing.T) {
	manyKeys := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		manyKeys = append(manyKeys, fmt.Sprintf("k%d: %d", i, i))
	}
	bigMap := "{" + strings.Join(manyKeys, ", ") + "}"

	for _, tc := range []struct {
		name         string
		setup        []string
		stmt         string
		set, removed int // PropertiesSet, PropertiesRemoved
	}{
		{"set a new property", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.b = 2", 1, 0},
		{"overwrite a value", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 2", 1, 1},
		{"set the same value", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 1", 0, 0},
		{"an equal integer and float are the same value", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 1.0", 0, 0},
		{"set a key twice", []string{"CREATE (:N {a: 0})"}, "MATCH (n:N) SET n.a = 1, n.a = 2", 1, 1},
		{"change then restore", []string{"CREATE (:N {a: 7})"}, "MATCH (n:N) SET n.a = 9, n.a = 7", 0, 0},
		{"add then remove a new key", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.b = 2 REMOVE n.b", 0, 0},
		{"remove then re-add the same value", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) REMOVE n.a SET n.a = 1", 0, 0},
		{"remove then re-add a different value", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) REMOVE n.a SET n.a = 2", 1, 1},
		{"remove a property", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) REMOVE n.a", 0, 1},
		{"SET to null removes", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) SET n.a = null", 0, 1},
		{"remove a property that is absent", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) REMOVE n.z", 0, 0},
		{"three properties on three nodes", []string{"UNWIND range(1,3) AS i CREATE (:N {i: i})"}, "MATCH (n:N) SET n.a = 1, n.b = 2, n.c = 3", 9, 0},
		{"replace all with a map", []string{"CREATE (:N {a: 1, b: 2, c: 3})"}, "MATCH (n:N) SET n = {a: 1, d: 4}", 1, 2},
		{"merge a map", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) SET n += {b: 2, c: 3}", 1, 0},
		{"delete a node with properties", []string{"CREATE (:N {a: 1, b: 2, c: 3})"}, "MATCH (n:N) DELETE n", 0, 3},
		{"modify then delete: original count", []string{"CREATE (:N {a: 1, b: 2, c: 3})"}, "MATCH (n:N) SET n.d = 4 REMOVE n.a DELETE n", 0, 3},
		{"overwrite then delete", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) SET n.a = 5 DELETE n", 0, 2},
		{"remove everything then delete", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) REMOVE n.a, n.b DELETE n", 0, 2},
		{"create, set, delete in one statement", nil, "CREATE (n:N {a: 1}) SET n.b = 2 DELETE n", 0, 0},
		{"set on a created node", nil, "CREATE (n:N {a: 1}) SET n.a = 2, n.b = 3", 2, 0},
		{"relationship: set and overwrite", []string{"CREATE (:A)-[:R {w: 1}]->(:B)"}, "MATCH ()-[r:R]->() SET r.w = 2, r.x = 3", 2, 1},
		{"relationship: change then restore", []string{"CREATE (:A)-[:R {w: 1}]->(:B)"}, "MATCH ()-[r:R]->() SET r.w = 9, r.w = 1", 0, 0},
		{"relationship: delete after modifying", []string{"CREATE (:A)-[:R {w: 1, v: 2}]->(:B)"}, "MATCH ()-[r:R]->() SET r.z = 3 REMOVE r.w DELETE r", 0, 2},
		{"detach delete with modified properties", []string{"CREATE (:N {a: 1, b: 2})-[:R {w: 1}]->(:M {c: 3})"}, "MATCH (n:N)-[r:R]->(m:M) SET n.a = 9, m.d = 4 DETACH DELETE n, m", 0, 4},
		{"many keys on one node", []string{"CREATE (:N {k0: 0, k1: 99})"}, "MATCH (n:N) SET n += " + bigMap, 39, 1},
		{"many keys, then restore some", []string{"CREATE (:N {k0: 0})"}, "MATCH (n:N) SET n += " + bigMap + " REMOVE n.k5, n.k20, n.k39", 36, 0},
		{"many keys then delete", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) SET n += " + bigMap + " DELETE n", 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newDB(t)
			for _, s := range tc.setup {
				if _, err := run(db, nil, s, nil); err != nil {
					t.Fatalf("setup %q: %v", s, err)
				}
			}
			res, err := run(db, nil, tc.stmt, nil)
			if err != nil {
				t.Fatal(err)
			}
			c := res.Counters
			if c.PropertiesSet != tc.set || c.PropertiesRemoved != tc.removed {
				t.Errorf("properties set/removed = %d/%d, want %d/%d", c.PropertiesSet, c.PropertiesRemoved, tc.set, tc.removed)
			}
		})
	}
}

// The distinct-label counters (LabelsAdded/LabelsRemoved) compare the labels a
// node had before the statement with those it has after, so they depend on the
// snapshot taken when a node's labels first change, including when its
// properties changed earlier in the same statement.
func TestLabelCountersWithPropertyChanges(t *testing.T) {
	for _, tc := range []struct {
		name         string
		setup        []string
		stmt         string
		added, drops int // LabelsAdded, LabelsRemoved (distinct labels)
	}{
		{"remove the last holder of a label", []string{"CREATE (:N)"}, "MATCH (n:N) REMOVE n:N", 0, 1},
		{"property then label removal", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 2 REMOVE n:N", 0, 1},
		{"label removal then property", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) REMOVE n:N SET n.a = 2", 0, 1},
		{"property then a brand new label", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 2, n:M", 1, 0},
		{"new label then property", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n:M SET n.a = 2", 1, 0},
		{"swap one label for another", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 2 REMOVE n:N SET n:M", 1, 1},
		{"label stays on another node", []string{"CREATE (:N {a: 1}), (:N {a: 2})"}, "MATCH (n:N {a: 1}) SET n.a = 9 REMOVE n:N", 0, 0},
		{"label already in the graph", []string{"CREATE (:N {a: 1}), (:M)"}, "MATCH (n:N) SET n.a = 9, n:M", 0, 0},
		{"modify, relabel, then delete", []string{"CREATE (:N {a: 1, b: 2})"}, "MATCH (n:N) SET n.a = 5 REMOVE n:N SET n:M DELETE n", 0, 1},
		{"modify then delete frees the label", []string{"CREATE (:N {a: 1})"}, "MATCH (n:N) SET n.a = 5 DELETE n", 0, 1},
		{"relabel two nodes at once", []string{"UNWIND range(1,2) AS i CREATE (:N {i: i})"}, "MATCH (n:N) SET n.x = 1 REMOVE n:N SET n:M", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newDB(t)
			for _, s := range tc.setup {
				if _, err := run(db, nil, s, nil); err != nil {
					t.Fatalf("setup %q: %v", s, err)
				}
			}
			res, err := run(db, nil, tc.stmt, nil)
			if err != nil {
				t.Fatal(err)
			}
			if c := res.Counters; c.LabelsAdded != tc.added || c.LabelsRemoved != tc.drops {
				t.Errorf("labels added/removed = %d/%d, want %d/%d", c.LabelsAdded, c.LabelsRemoved, tc.added, tc.drops)
			}
		})
	}
}
