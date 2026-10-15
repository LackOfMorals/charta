package graingraph_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graingraph/graingraph"
	_ "modernc.org/sqlite"
)

func TestSubqueryExpressionsRejectUpdates(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := db.RunQuery(ctx, `CREATE (:Keep)`, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`RETURN EXISTS { WHEN true THEN { CREATE (:Pwned) } }`,
		`MATCH (n) WHERE COUNT { WHEN true THEN { DETACH DELETE n } } >= 0 RETURN 1`,
		`RETURN COLLECT { WHEN true THEN { MATCH (m) RETURN m } ELSE { CREATE (x:X) RETURN x } }`,
		`CREATE INDEX i IF NOT EXISTS FOR (n:X) ON (n.y) OPTIONS {a: EXISTS { MATCH (m) DETACH DELETE m }}`,
		`CREATE CONSTRAINT c IF NOT EXISTS FOR (n:X) REQUIRE n.y IS UNIQUE OPTIONS {a: COUNT { MATCH (m) RETURN m }}`,
	} {
		if _, err := db.RunQuery(ctx, q, nil); err == nil {
			t.Errorf("%s: expected an analysis error", q)
		}
	}
	res, _ := db.RunQuery(ctx, `MATCH (n) RETURN count(n)`, nil)
	recs, _ := res.Collect(ctx)
	if recs[0].Values()[0] != int64(1) {
		t.Fatalf("graph changed: %v", recs[0].Values()[0])
	}
}

func TestCorruptSchemaRowsAreSkippedAndDroppable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "g.db")
	db, err := graingraph.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunQuery(ctx, `CREATE CONSTRAINT c0 FOR (n:P) REQUIRE n.x IS UNIQUE`, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for name, def := range map[string]string{
		"c1": `{"constraint":true,"kind":"EXISTENCE","entity":"NODE","targets":[],"props":["x"]}`,
		"c2": `{"kind":"RANGE","entity":"NODE","targets":["A"],"props":["a"],"backingIndex":"x; DROP TABLE edges; --"}`,
	} {
		if _, err := raw.Exec(`INSERT INTO charta_schema(name, def) VALUES (?, ?)`, name, def); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	db, err = graingraph.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.RunQuery(ctx, `CREATE (n)`, nil); err != nil {
		t.Fatalf("write with corrupt rows: %v", err)
	}
	if _, err := db.RunQuery(ctx, `DROP INDEX c2`, nil); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.RunQuery(ctx, `MATCH ()-[r]->() RETURN count(r)`, nil); err != nil {
		t.Fatalf("edges table damaged: %v", err)
	}
}

func TestImportSchemaCannotRunSubqueries(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := db.RunQuery(ctx, `CREATE (:Keep)`, nil); err != nil {
		t.Fatal(err)
	}
	doc := `{"nodes":[],"edges":[],"schema":["CREATE INDEX i IF NOT EXISTS FOR (n:X) ON (n.y) OPTIONS {a: EXISTS { MATCH (m) DETACH DELETE m }}"]}`
	if err := db.Import(ctx, strings.NewReader(doc), graingraph.FormatJSON); err == nil {
		t.Fatal("expected the import to be rejected")
	}
	res, _ := db.RunQuery(ctx, `MATCH (n) RETURN count(n)`, nil)
	recs, _ := res.Collect(ctx)
	if recs[0].Values()[0] != int64(1) {
		t.Fatal("existing data was changed by an import schema entry")
	}
}
