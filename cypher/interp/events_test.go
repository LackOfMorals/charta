package interp_test

import "testing"

// Events count side effects the way Neo4j's query statistics do: as they
// happen, never netted out. Each expectation is what a Neo4j 2026.07 server
// returned for the same statement; the net counters (Counters.NodesCreated and
// the rest) are checked alongside where they differ, to show the contrast.
func TestEventCounters(t *testing.T) {
	type want struct{ nodesC, nodesD, relsC, relsD, props int }
	for _, tc := range []struct {
		name  string
		setup []string
		stmt  string
		ev    want
	}{
		// nodes and relationships
		{"node created then deleted", nil, "CREATE (n:A) DELETE n", want{1, 1, 0, 0, 0}},
		{"node with properties created then deleted", nil, "CREATE (n:A {a: 1, b: 2}) DELETE n", want{1, 1, 0, 0, 2}},
		{"created, property set, then deleted", nil, "CREATE (n:A) SET n.a = 1 DELETE n", want{1, 1, 0, 0, 1}},
		{"relationship created then deleted", []string{"CREATE (:A), (:B)"}, "MATCH (a:A), (b:B) CREATE (a)-[r:R {w: 1}]->(b) DELETE r", want{0, 0, 1, 1, 1}},
		{"two nodes and a relationship, detach deleted", nil, "CREATE (a:A)-[:R {w: 1}]->(b:B) DETACH DELETE a, b", want{2, 2, 1, 1, 1}},
		{"existing node deleted", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) DELETE n", want{0, 1, 0, 0, 0}},
		{"detach delete counts each relationship", []string{"CREATE (a:A)-[:R {w: 1}]->(:B), (a)-[:R {w: 2}]->(:B)"}, "MATCH (a:A) DETACH DELETE a", want{0, 1, 0, 2, 0}},
		{"existing relationship with properties deleted", []string{"CREATE (:A)-[:R {w: 1, v: 2}]->(:B)"}, "MATCH ()-[r:R]->() DELETE r", want{0, 0, 0, 1, 0}},
		{"relationship created without properties", []string{"CREATE (:A), (:B)"}, "MATCH (a:A), (b:B) CREATE (a)-[:R]->(b)", want{0, 0, 1, 0, 0}},
		{"MERGE that matches", []string{"CREATE (:A {k: 1})"}, "MERGE (:A {k: 1})", want{0, 0, 0, 0, 0}},
		{"MERGE that creates, with ON CREATE SET", nil, "MERGE (n:A {k: 1}) ON CREATE SET n.c = 5", want{1, 0, 0, 0, 2}},

		// properties on nodes
		{"CREATE skips a null property", nil, "CREATE (:A {a: null, b: 1})", want{1, 0, 0, 0, 1}},
		{"set a property to the value it already has", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) SET n.a = 1", want{0, 0, 0, 0, 1}},
		{"set a key twice", []string{"CREATE (:A {a: 0})"}, "MATCH (n:A) SET n.a = 1, n.a = 2", want{0, 0, 0, 0, 2}},
		{"change then restore", []string{"CREATE (:A {a: 7})"}, "MATCH (n:A) SET n.a = 9, n.a = 7", want{0, 0, 0, 0, 2}},
		{"add then remove a new property", []string{"CREATE (:A)"}, "MATCH (n:A) SET n.b = 2 REMOVE n.b", want{0, 0, 0, 0, 2}},
		{"overwrite a value", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) SET n.a = 2", want{0, 0, 0, 0, 1}},
		{"removing a property counts as a set", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) REMOVE n.a", want{0, 0, 0, 0, 1}},
		{"SET to null on an existing property", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) SET n.a = null", want{0, 0, 0, 0, 1}},
		{"SET to null on an absent property", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) SET n.z = null", want{0, 0, 0, 0, 0}},
		{"REMOVE an absent property", []string{"CREATE (:A {a: 1})"}, "MATCH (n:A) REMOVE n.z", want{0, 0, 0, 0, 0}},
		{"a property on a created node, then overwritten", nil, "CREATE (n:A {a: 1}) SET n.a = 2", want{1, 0, 0, 0, 2}},
		{"create, set, remove, delete", nil, "CREATE (n:A {a: 1}) SET n.b = 2 REMOVE n.a DELETE n", want{1, 1, 0, 0, 3}},
		{"one property on three nodes", []string{"UNWIND range(1,3) AS i CREATE (:A {i: i})"}, "MATCH (n:A) SET n.x = 1", want{0, 0, 0, 0, 3}},
		{"a list value", []string{"CREATE (:A)"}, "MATCH (n:A) SET n.l = [1, 2, 3]", want{0, 0, 0, 0, 1}},

		// map assignment
		{"replace all properties with a map", []string{"CREATE (:A {a: 1, b: 2, c: 3})"}, "MATCH (n:A) SET n = {a: 1, d: 4}", want{0, 0, 0, 0, 4}},
		{"replace with an empty map", []string{"CREATE (:A {a: 1, b: 2})"}, "MATCH (n:A) SET n = {}", want{0, 0, 0, 0, 2}},
		{"merge a map", []string{"CREATE (:A {a: 1, b: 2})"}, "MATCH (n:A) SET n += {b: 2, c: 3}", want{0, 0, 0, 0, 2}},
		{"merge a map with a null for an existing key", []string{"CREATE (:A {a: 1, b: 2})"}, "MATCH (n:A) SET n += {a: null}", want{0, 0, 0, 0, 1}},

		// properties on relationships
		{"relationship: same value", []string{"CREATE (:A)-[:R {w: 1}]->(:B)"}, "MATCH ()-[r:R]->() SET r.w = 1", want{0, 0, 0, 0, 1}},
		{"relationship: remove a property", []string{"CREATE (:A)-[:R {w: 1}]->(:B)"}, "MATCH ()-[r:R]->() REMOVE r.w", want{0, 0, 0, 0, 1}},
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
			e := res.Counters.Events
			got := want{e.NodesCreated, e.NodesDeleted, e.RelationshipsCreated, e.RelationshipsDeleted, e.PropertiesSet}
			if got != tc.ev {
				t.Errorf("events nodes +%d/-%d rels +%d/-%d props %d, want +%d/-%d +%d/-%d %d",
					got.nodesC, got.nodesD, got.relsC, got.relsD, got.props,
					tc.ev.nodesC, tc.ev.nodesD, tc.ev.relsC, tc.ev.relsD, tc.ev.props)
			}
		})
	}
}

// The net counters keep the TCK meaning, which the harness checks exactly: a
// node created and deleted in one statement leaves no trace.
func TestNetCountersUnchangedByEvents(t *testing.T) {
	db := newDB(t)
	res, err := run(db, nil, "CREATE (n:A {a: 1}) DELETE n", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Counters
	if c.NodesCreated != 0 || c.NodesDeleted != 0 || c.PropertiesSet != 0 || c.LabelsAdded != 0 {
		t.Errorf("net counters %+v, want all zero", c)
	}
	if e := c.Events; e.NodesCreated != 1 || e.NodesDeleted != 1 || e.PropertiesSet != 1 {
		t.Errorf("events %+v, want a node created and deleted and one property set", e)
	}
}
