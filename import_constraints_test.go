package charta_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta"
)

func nodeCount(t *testing.T, db *charta.DB) int64 {
	t.Helper()
	return count(t, db, "MATCH (n) RETURN count(n)")
}

func mustRun(t *testing.T, db *charta.DB, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := db.RunQuery(context.Background(), q, nil); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestImportEnforcesConstraints(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name, setup, doc, kind string
	}{
		{"duplicates inside the file", "CREATE CONSTRAINT FOR (p:Person) REQUIRE p.email IS UNIQUE",
			`{"nodes":[{"id":"1","labels":["Person"],"props":{"email":"a"}},{"id":"2","labels":["Person"],"props":{"email":"a"}}]}`, "UNIQUENESS"},
		{"duplicate of existing data", "CREATE CONSTRAINT FOR (p:Person) REQUIRE p.email IS UNIQUE",
			`{"nodes":[{"id":"1","labels":["Person"],"props":{"email":"taken"}}]}`, "UNIQUENESS"},
		{"missing required property", "CREATE CONSTRAINT FOR (p:Person) REQUIRE p.name IS NOT NULL",
			`{"nodes":[{"id":"1","labels":["Person"],"props":{"age":3}}]}`, "EXISTENCE"},
		{"wrong type", "CREATE CONSTRAINT FOR (p:Person) REQUIRE p.age IS :: INTEGER",
			`{"nodes":[{"id":"1","labels":["Person"],"props":{"age":"old"}}]}`, "TYPE"},
		{"vector dimension", "CREATE VECTOR INDEX v FOR (p:Person) ON (p.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}",
			`{"nodes":[{"id":"1","labels":["Person"],"props":{"e":[1,2,3]}}]}`, "VECTOR"},
		{"relationship constraint", "CREATE CONSTRAINT FOR ()-[r:RATED]-() REQUIRE r.stars IS NOT NULL",
			`{"nodes":[{"id":"1","labels":["A"]},{"id":"2","labels":["B"]}],"edges":[{"type":"RATED","startId":"1","endId":"2","props":{}}]}`, "EXISTENCE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openMemDB(t)
			mustRun(t, db, tt.setup, "CREATE (:Person {email: 'taken', name: 'x', age: 1})")
			before := nodeCount(t, db)
			err := db.Import(ctx, strings.NewReader(tt.doc), charta.FormatJSON)
			var cv *charta.ErrConstraintViolation
			if !errors.As(err, &cv) || cv.Kind != tt.kind {
				t.Fatalf("got %v, want a %s violation", err, tt.kind)
			}
			if after := nodeCount(t, db); after != before {
				t.Errorf("a failed import must be atomic: %d nodes before, %d after", before, after)
			}
		})
	}
}

func TestImportCSVEnforcesConstraints(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	mustRun(t, db, "CREATE CONSTRAINT FOR (p:Person) REQUIRE p.email IS UNIQUE")
	nodes := ":ID,:LABEL,email:string\n1,Person,a\n2,Person,a\n"
	err := db.Import(ctx, strings.NewReader(nodes), charta.FormatCSVNodes)
	var cv *charta.ErrConstraintViolation
	if !errors.As(err, &cv) || cv.Kind != "UNIQUENESS" {
		t.Fatalf("csv nodes: %v", err)
	}
	if nodeCount(t, db) != 0 {
		t.Error("nothing may be imported")
	}
	ok := ":ID,:LABEL,email:string\n1,Person,a\n2,Person,b\n"
	if err := db.Import(ctx, strings.NewReader(ok), charta.FormatCSVNodes); err != nil {
		t.Fatal(err)
	}

	mustRun(t, db, "CREATE CONSTRAINT FOR ()-[r:KNOWS]-() REQUIRE r.since IS NOT NULL")
	ids := count(t, db, "MATCH (p:Person {email: 'a'}) RETURN id(p)")
	ids2 := count(t, db, "MATCH (p:Person {email: 'b'}) RETURN id(p)")
	edges := ":START_ID,:END_ID,:TYPE\n" + itoa(ids) + "," + itoa(ids2) + ",KNOWS\n"
	err = db.Import(ctx, strings.NewReader(edges), charta.FormatCSVEdges)
	if !errors.As(err, &cv) || cv.Kind != "EXISTENCE" || cv.EntityType != "RELATIONSHIP" {
		t.Fatalf("csv edges: %v", err)
	}
	if count(t, db, "MATCH ()-[r]->() RETURN count(r)") != 0 {
		t.Error("nothing may be imported")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestImportedVectorsAreSearchable(t *testing.T) {
	ctx := context.Background()
	db, _ := openFileDB(t)
	mustRun(t, db, vecIndexDDL, "CREATE (:Doc {id: 1, e: [9.0, 9.0, 9.0]})")
	// Build the cache, then import data behind the interpreter's back.
	if got := search(t, dbRunner{db}, []float64{0, 0, 0}, 5); !sameIDs(got, []int64{1}) {
		t.Fatalf("before the import %v", got)
	}
	doc := `{"nodes":[{"id":"a","labels":["Doc"],"props":{"id":2,"e":[0,0,1]}},{"id":"b","labels":["Doc"],"props":{"id":3,"e":[5,5,5]}}]}`
	if err := db.Import(ctx, strings.NewReader(doc), charta.FormatJSON); err != nil {
		t.Fatal(err)
	}
	if got := search(t, dbRunner{db}, []float64{0, 0, 0}, 5); !sameIDs(got, []int64{2, 3, 1}) {
		t.Errorf("after the import %v", got)
	}
}

func TestExportImportCarriesTheSchema(t *testing.T) {
	ctx := context.Background()
	src := openMemDB(t)
	setupSchema(t, src)
	mustRun(t, src,
		"CREATE CONSTRAINT rated FOR ()-[r:RATED]-() REQUIRE r.stars IS :: INTEGER",
		"CREATE CONSTRAINT person_key FOR (p:Person) REQUIRE (p.name, p.email) IS NODE KEY",
		"CREATE INDEX rated_idx FOR ()-[r:RATED]-() ON (r.stars)",
		"CREATE INDEX odd FOR (n:`Odd label`) ON (n.`a prop`)")
	var buf bytes.Buffer
	if err := src.Export(ctx, &buf, charta.ExportFormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"schema"`) {
		t.Fatalf("the export should carry the schema: %s", buf.String())
	}

	dst := openMemDB(t)
	if err := dst.Import(ctx, bytes.NewReader(buf.Bytes()), charta.FormatJSON); err != nil {
		t.Fatal(err)
	}
	checkSchemaEnforced(t, dst)
	want, _ := src.ListSchema(ctx)
	got, _ := dst.ListSchema(ctx)
	key := func(s charta.SchemaInfo) string {
		return s.Name + "|" + s.Type + "|" + s.EntityType + "|" + strings.Join(s.Labels, ",") + "|" + strings.Join(s.Properties, ",")
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[key(s)] = true
	}
	for _, s := range want {
		if !seen[key(s)] {
			t.Errorf("schema object %s was not carried over; got %v", key(s), got)
		}
	}

	// Importing the same file again is harmless for the schema (IF NOT EXISTS) but the
	// data now conflicts with the unique constraint, atomically.
	err := dst.Import(ctx, bytes.NewReader(buf.Bytes()), charta.FormatJSON)
	var cv *charta.ErrConstraintViolation
	if !errors.As(err, &cv) {
		t.Errorf("re-importing duplicates must violate the constraint: %v", err)
	}
}

func TestImportRejectsNonSchemaStatements(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	mustRun(t, db, "CREATE (:Keep)")
	for _, stmt := range []string{"MATCH (n) DETACH DELETE n", "CREATE (:Evil)", "RETURN 1"} {
		doc := `{"nodes":[],"schema":["` + stmt + `"]}`
		if err := db.Import(ctx, strings.NewReader(doc), charta.FormatJSON); err == nil {
			t.Errorf("%q must be rejected", stmt)
		}
	}
	if nodeCount(t, db) != 1 {
		t.Error("the graph must be untouched")
	}
}
