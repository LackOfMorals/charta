package graphlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LackOfMorals/graphlite/v2"
)

func TestUnsupportedConstructIsErrUnsupportedCypher(t *testing.T) {
	db := openMemDB(t)
	_, err := db.RunQuery(context.Background(), "MATCH (a) (()-[:R]->()){1,3} (b) RETURN a", nil)
	var target *graphlite.ErrUnsupportedCypher
	if !errors.As(err, &target) {
		t.Fatalf("got %T %v, want *ErrUnsupportedCypher", err, err)
	}
}

func TestMaxPathHopsOption(t *testing.T) {
	ctx := context.Background()
	db, err := graphlite.Open(":memory:", graphlite.WithMaxPathHops(2))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.RunQuery(ctx, "CREATE (:N)-[:R]->(:N)", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunQuery(ctx, "MATCH (a:N)-[:R*1..5]->(b) RETURN b", nil); err == nil {
		t.Error("explicit bound above the cap should fail")
	}
}
