package interp

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta/store"
)

// recordingDB remembers the last query so a test can ask SQLite how it would run it.
type recordingDB struct {
	DB
	query string
	args  []any
}

func (r *recordingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	r.query, r.args = q, args
	return r.DB.QueryContext(ctx, q, args...)
}

func queryPlan(t *testing.T, db *store.SQLiteStore, q string, args []any) string {
	t.Helper()
	rows, err := db.DB().Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, " | ")
}

// The SQL the interpreter issues for a property lookup must use an index when
// one exists, whichever way the index came to be.
func TestPropertyLookupsUseIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:", store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runInternal(t, db, "UNWIND range(1, 800) AS i CREATE (:P {k: i, u: i, a: i, ak: i})", nil)
	runInternal(t, db, "UNWIND range(1, 800) AS i MATCH (x:P {k: i}) CREATE (x)-[:R {w: i}]->(x)", nil)

	scanPlan := func(prop string) string {
		rec := &recordingDB{DB: db.DB()}
		g := newGraph(ctx, rec)
		if _, err := g.scanNodes("P", []propHint{{prop, int64(5)}}); err != nil {
			t.Fatal(err)
		}
		return queryPlan(t, db, rec.query, rec.args)
	}
	relPlan := func(prop string) string {
		rec := &recordingDB{DB: db.DB()}
		g := newGraph(ctx, rec)
		if _, err := g.scanRels("R", []propHint{{prop, int64(5)}}); err != nil {
			t.Fatal(err)
		}
		return queryPlan(t, db, rec.query, rec.args)
	}

	if p := scanPlan("k"); !strings.Contains(p, "SCAN") || strings.Contains(p, "gl_idx") {
		t.Errorf("without an index the lookup should scan: %s", p)
	}

	runInternal(t, db, "CREATE INDEX FOR (n:P) ON (n.k)", nil)
	if p := scanPlan("k"); !strings.Contains(p, "USING INDEX gl_idx_") {
		t.Errorf("declared index not used: %s", p)
	}

	runInternal(t, db, "CREATE CONSTRAINT FOR (n:P) REQUIRE n.u IS UNIQUE", nil)
	if p := scanPlan("u"); !strings.Contains(p, "USING INDEX gl_idx_") {
		t.Errorf("a uniqueness constraint's backing index not used: %s", p)
	}

	// Automatic index: the advisor creates it after a few scans.
	eng := &Engine{}
	for i := 0; i < indexScansBeforeIndex; i++ {
		eng.noteScan(ctx, db.DB(), "a", false)
	}
	if p := scanPlan("a"); !strings.Contains(p, "USING INDEX idx_auto_np_") {
		t.Errorf("automatic index not used: %s", p)
	}

	// Without a property index SQLite can only narrow by type and then filters.
	if p := relPlan("w"); strings.Contains(p, "gl_idx") {
		t.Errorf("no relationship property index exists yet: %s", p)
	}
	runInternal(t, db, "CREATE INDEX FOR ()-[r:R]-() ON (r.w)", nil)
	if p := relPlan("w"); !strings.Contains(p, "USING INDEX gl_idx_") {
		t.Errorf("relationship property index not used: %s", p)
	}
}
