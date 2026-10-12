package interp_test

import "testing"

// NodeLabelsAdded/NodeLabelsRemoved follow Neo4j's query statistics: one count
// per node and label, at the moment the label changes, not netted out over the
// statement, and deleting a node removes no labels. The expectations are what a
// Neo4j 2026.07 server reports for the same statements. LabelsAdded and
// LabelsRemoved keep their TCK meaning (distinct labels appearing in or
// vanishing from the graph) and are checked alongside to show the difference.
func TestNodeLabelCounters(t *testing.T) {
	for _, tc := range []struct {
		name            string
		setup           []string
		stmt            string
		added, removed  int // NodeLabelsAdded, NodeLabelsRemoved
		tckAdd, tckDrop int // LabelsAdded, LabelsRemoved
	}{
		{"five nodes, one label each", nil, "UNWIND range(1,5) AS i CREATE (:A {i:i})", 5, 0, 1, 0},
		{"node with two labels", nil, "CREATE (:A:B)", 2, 0, 2, 0},
		{"duplicate label in CREATE", nil, "CREATE (:A:A)", 1, 0, 1, 0},
		{"label the node already has", []string{"CREATE (:A)"}, "MATCH (n:A) SET n:A", 0, 0, 0, 0},
		{"two labels, one already present", []string{"CREATE (:A)"}, "MATCH (n:A) SET n:A:B", 1, 0, 1, 0},
		{"new label on three nodes", []string{"UNWIND range(1,3) AS i CREATE (:A)"}, "MATCH (n:A) SET n:B", 3, 0, 1, 0},
		{"label that already exists elsewhere", []string{"CREATE (:A), (:B)"}, "MATCH (n:A) SET n:B", 1, 0, 0, 0},
		{"remove a label from three nodes", []string{"UNWIND range(1,3) AS i CREATE (:A:B)"}, "MATCH (n:A) REMOVE n:B", 0, 3, 0, 1},
		{"remove a label the node lacks", []string{"CREATE (:A)"}, "MATCH (n:A) REMOVE n:Z", 0, 0, 0, 0},
		{"remove two labels", []string{"CREATE (:A:B:C)"}, "MATCH (n:A) REMOVE n:B:C", 0, 2, 0, 2},
		{"deleting nodes removes no labels", []string{"UNWIND range(1,3) AS i CREATE (:A:B)"}, "MATCH (n:A) DELETE n", 0, 0, 0, 2},
		{"create then delete in one statement", nil, "CREATE (n:A:B) DELETE n", 2, 0, 0, 0},
		{"add then remove the same label", []string{"CREATE (:A)"}, "MATCH (n:A) SET n:B REMOVE n:B", 1, 1, 0, 0},
		{"remove then re-add the same label", []string{"CREATE (:A:B)"}, "MATCH (n:A) REMOVE n:B SET n:B", 1, 1, 0, 0},
		{"create then SET another label", nil, "CREATE (n:A) SET n:B", 2, 0, 2, 0},
		{"MERGE that creates", nil, "MERGE (:A {k: 1})", 1, 0, 1, 0},
		{"MERGE that matches", []string{"CREATE (:A {k: 1})"}, "MERGE (:A {k: 1})", 0, 0, 0, 0},
		{"MERGE ON CREATE SET a label", nil, "MERGE (n:A {k: 1}) ON CREATE SET n:B", 2, 0, 2, 0},
		{"delete a node whose label was added in the statement", []string{"CREATE (:A)"}, "MATCH (n:A) SET n:B DELETE n", 1, 0, 0, 1},
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
			if c.NodeLabelsAdded != tc.added || c.NodeLabelsRemoved != tc.removed {
				t.Errorf("node labels added/removed = %d/%d, want %d/%d", c.NodeLabelsAdded, c.NodeLabelsRemoved, tc.added, tc.removed)
			}
			if c.LabelsAdded != tc.tckAdd || c.LabelsRemoved != tc.tckDrop {
				t.Errorf("TCK labels added/removed = %d/%d, want %d/%d", c.LabelsAdded, c.LabelsRemoved, tc.tckAdd, tc.tckDrop)
			}
		})
	}
}
