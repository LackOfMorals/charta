package graphlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LackOfMorals/graphlite/v2"
)

func TestSchemaGoAPI(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	if err := db.CreatePropertyIndex(ctx, "Person", "name"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreatePropertyIndex(ctx, "Person", "name"); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := db.CreateUniqueConstraint(ctx, "Person", "email"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUniqueConstraint(ctx, "Person", "email"); err != nil {
		t.Fatal(err)
	}

	info, err := db.ListSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var idx, con *graphlite.SchemaInfo
	for i := range info {
		switch {
		case !info[i].IsConstraint && info[i].Type == "RANGE" && info[i].OwningConstraint == "":
			idx = &info[i]
		case info[i].IsConstraint:
			con = &info[i]
		}
	}
	if idx == nil || idx.EntityType != "NODE" || strings.Join(idx.Labels, ",") != "Person" || strings.Join(idx.Properties, ",") != "name" {
		t.Errorf("index not listed correctly: %+v", info)
	}
	if con == nil || con.Type != "UNIQUENESS" || strings.Join(con.Properties, ",") != "email" {
		t.Errorf("constraint not listed correctly: %+v", info)
	}
	backing := 0
	for _, s := range info {
		if s.OwningConstraint == con.Name {
			backing++
		}
	}
	if backing != 1 {
		t.Errorf("the constraint should own exactly one index, found %d", backing)
	}
	// The built-in lookup indexes are listed too.
	lookups := 0
	for _, s := range info {
		if s.Type == "LOOKUP" {
			lookups++
		}
	}
	if lookups != 2 {
		t.Errorf("expected the two lookup indexes, found %d", lookups)
	}

	if err := db.DropConstraint(ctx, con.Name); err != nil {
		t.Fatal(err)
	}
	if err := db.DropIndex(ctx, idx.Name); err != nil {
		t.Fatal(err)
	}
	if err := db.DropIndex(ctx, "does_not_exist"); err != nil { // no-op
		t.Fatal(err)
	}
	after, _ := db.ListSchema(ctx)
	for _, s := range after {
		if s.Type != "LOOKUP" {
			t.Errorf("%+v should have been dropped", s)
		}
	}
}

func TestSchemaGoAPIQuotesNames(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	// A name with spaces and a backtick must be treated as one identifier, not
	// as Cypher.
	if err := db.CreateUniqueConstraint(ctx, "Odd `label` {x}", "a property) //"); err != nil {
		t.Fatalf("quoted names should work: %v", err)
	}
	info, _ := db.ListSchema(ctx)
	found := false
	for _, s := range info {
		if s.IsConstraint && len(s.Labels) == 1 && s.Labels[0] == "Odd `label` {x}" && s.Properties[0] == "a property) //" {
			found = true
		}
	}
	if !found {
		t.Errorf("constraint with unusual names not stored verbatim: %+v", info)
	}
	if err := db.CreatePropertyIndex(ctx, "", "p"); err == nil {
		t.Error("an empty label must be rejected")
	}
	if err := db.CreatePropertyIndex(ctx, "L", "bad\x00name"); err == nil {
		t.Error("a NUL in a name must be rejected")
	}
}

func TestConstraintViolationIsAStructuredError(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	if err := db.CreateUniqueConstraint(ctx, "Person", "email"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunQuery(ctx, "CREATE (:Person {email: 'a@x.com'})", nil); err != nil {
		t.Fatal(err)
	}
	_, err := db.RunQuery(ctx, "CREATE (:Person {email: 'a@x.com'})", nil)
	var cv *graphlite.ErrConstraintViolation
	if !errors.As(err, &cv) {
		t.Fatalf("got %T %v, want *ErrConstraintViolation", err, err)
	}
	if cv.Kind != "UNIQUENESS" || cv.EntityType != "NODE" || cv.Label != "Person" ||
		len(cv.Properties) != 1 || cv.Properties[0] != "email" || cv.Name == "" || !strings.Contains(cv.Message, "already exists") {
		t.Errorf("unexpected details: %+v", cv)
	}
	if !strings.Contains(cv.Error(), "email") {
		t.Errorf("Error() = %q", cv.Error())
	}

	// Other constraint kinds and vector indexes report through the same type.
	for _, tc := range []struct{ setup, bad, kind string }{
		{"CREATE CONSTRAINT FOR (n:A) REQUIRE n.p IS NOT NULL", "CREATE (:A {q: 1})", "EXISTENCE"},
		{"CREATE CONSTRAINT FOR (n:B) REQUIRE n.p IS :: INTEGER", "CREATE (:B {p: 'x'})", "TYPE"},
		{"CREATE CONSTRAINT FOR (n:C) REQUIRE (n.a, n.b) IS NODE KEY", "CREATE (:C {a: 1})", "KEY"},
		{"CREATE VECTOR INDEX v FOR (n:D) ON (n.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}", "CREATE (:D {e: [1, 2, 3]})", "VECTOR"},
	} {
		if _, err := db.RunQuery(ctx, tc.setup, nil); err != nil {
			t.Fatal(err)
		}
		_, err := db.RunQuery(ctx, tc.bad, nil)
		if !errors.As(err, &cv) || cv.Kind != tc.kind {
			t.Errorf("%s: got %v, want a %s violation", tc.bad, err, tc.kind)
		}
	}
	// A violation inside an explicit transaction is the same error and leaves
	// the transaction usable for rollback.
	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Run(ctx, "CREATE (:Person {email: 'a@x.com'})", nil); !errors.As(err, &cv) {
		t.Errorf("in a transaction: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
