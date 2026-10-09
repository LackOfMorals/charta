package charta_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LackOfMorals/charta"
)

func setupSchema(t *testing.T, db *charta.DB) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		"CREATE CONSTRAINT person_email FOR (p:Person) REQUIRE p.email IS UNIQUE",
		"CREATE CONSTRAINT person_name FOR (p:Person) REQUIRE p.name IS NOT NULL",
		"CREATE INDEX person_age FOR (p:Person) ON (p.age)",
		"CREATE VECTOR INDEX doc_vec FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 2, `vector.similarity_function`: 'euclidean'}}",
		"CREATE (:Person {name: 'A', email: 'a@x.com', age: 30}), (:Doc {e: [0.0, 0.0]}), (:Doc {e: [3.0, 4.0]})",
	} {
		if _, err := db.RunQuery(ctx, q, nil); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func checkSchemaEnforced(t *testing.T, db *charta.DB) {
	t.Helper()
	ctx := context.Background()
	info, err := db.ListSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range info {
		names[s.Name] = true
	}
	for _, want := range []string{"person_email", "person_name", "person_age", "doc_vec"} {
		if !names[want] {
			t.Errorf("schema object %q is missing: %+v", want, info)
		}
	}
	var cv *charta.ErrConstraintViolation
	if _, err := db.RunQuery(ctx, "CREATE (:Person {name: 'B', email: 'a@x.com'})", nil); !errors.As(err, &cv) || cv.Kind != "UNIQUENESS" {
		t.Errorf("uniqueness not enforced: %v", err)
	}
	if _, err := db.RunQuery(ctx, "CREATE (:Person {email: 'c@x.com'})", nil); !errors.As(err, &cv) || cv.Kind != "EXISTENCE" {
		t.Errorf("existence not enforced: %v", err)
	}
	if _, err := db.RunQuery(ctx, "CREATE (:Doc {e: [1.0]})", nil); !errors.As(err, &cv) || cv.Kind != "VECTOR" {
		t.Errorf("vector dimension not enforced: %v", err)
	}
	matches, err := db.VectorSearch(ctx, "doc_vec", []float64{0, 0}, 2)
	if err != nil || len(matches) != 2 || matches[0].Score != 1 {
		t.Errorf("vector search: %v %v", matches, err)
	}
}

func TestSchemaSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "g.db")
	db, err := charta.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setupSchema(t, db)
	checkSchemaEnforced(t, db)
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := charta.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	checkSchemaEnforced(t, reopened) // nothing is warmed up: everything comes from the file
}

func TestSchemaSurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	for _, src := range []string{":memory:", filepath.Join(t.TempDir(), "src.db")} {
		db, err := charta.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		setupSchema(t, db)
		snap := filepath.Join(t.TempDir(), "snap.db")
		if err := db.Snapshot(snap); err != nil {
			t.Fatal(err)
		}
		db.Close(ctx)

		copyDB, err := charta.Open(snap)
		if err != nil {
			t.Fatal(err)
		}
		checkSchemaEnforced(t, copyDB)
		copyDB.Close(ctx)
	}
}
