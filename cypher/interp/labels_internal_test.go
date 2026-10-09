package interp

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/store"
)

// node_labels is written by the code that writes nodes.labels, not by triggers,
// so every write path must keep the two in step.
func TestNodeLabelsStayInSync(t *testing.T) {
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE (:A), (:A:B), (:A:B:C), ()",
		"UNWIND range(1, 20) AS i CREATE (:Bulk {i: i})",
		"MATCH (n:A) SET n:Z",
		"MATCH (n:B) REMOVE n:A",
		"MATCH (n {i: 3}) SET n:A:B",
		"MATCH (n:Bulk) WHERE n.i < 5 SET n:Small",
		"MATCH (n:Bulk) WHERE n.i > 18 DETACH DELETE n",
		"MATCH (n:C) SET n:A, n:A",
		"MERGE (:M:N {k: 1})",
		"MATCH (n:Z) REMOVE n:Z",
	} {
		runInternal(t, db, q, nil)
	}
	ctx := context.Background()
	want := map[int64]string{}
	rows, err := db.DB().QueryContext(ctx, `SELECT id, labels FROM nodes`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var l string
		if err := rows.Scan(&id, &l); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(l, ",")
		if l == "" {
			parts = nil
		}
		sort.Strings(parts)
		want[id] = strings.Join(parts, ",")
	}
	rows.Close()
	got := map[int64][]string{}
	rows, err = db.DB().QueryContext(ctx, `SELECT node_id, label FROM node_labels`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var l string
		if err := rows.Scan(&id, &l); err != nil {
			t.Fatal(err)
		}
		got[id] = append(got[id], l)
	}
	rows.Close()
	for id, w := range want {
		g := got[id]
		sort.Strings(g)
		if strings.Join(g, ",") != w {
			t.Errorf("node %d: nodes.labels %q, node_labels %v", id, w, g)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("node_labels has rows for deleted node %d", id)
		}
	}
	if len(want) < 20 {
		t.Fatalf("expected a populated graph, got %d nodes", len(want))
	}
}
