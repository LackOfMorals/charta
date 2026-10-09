// Command basic shows the core charta workflow: create nodes and relationships,
// query them, update them, and remove them again.
//
//	go run ./examples/basic
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/LackOfMorals/charta"
)

func main() {
	ctx := context.Background()

	// ":memory:" is a transient database; pass a file path to persist it.
	db, err := charta.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close(ctx)

	// Create two nodes and a relationship. Parameters keep values out of the
	// query text.
	res, err := db.RunQuery(ctx, `
		CREATE (a:Person {name: $alice, born: 1990})
		CREATE (b:Person {name: $bob, born: 1985})
		CREATE (a)-[:KNOWS {since: 2015}]->(b)`,
		map[string]any{"alice": "Alice", "bob": "Bob"})
	if err != nil {
		log.Fatal(err)
	}
	summary, err := res.Consume(ctx)
	if err != nil {
		log.Fatal(err)
	}
	c := summary.Counters()
	fmt.Printf("created %d nodes and %d relationship\n", c.NodesCreated(), c.RelationshipsCreated())

	// Query: walk the result with Next/Record.
	res, err = db.RunQuery(ctx, `
		MATCH (p:Person)-[k:KNOWS]->(friend:Person)
		RETURN p.name AS person, friend.name AS friend, k.since AS since`, nil)
	if err != nil {
		log.Fatal(err)
	}
	for res.Next(ctx) {
		rec := res.Record()
		person, _, _ := charta.GetRecordValue[string](rec, "person")
		friend, _, _ := charta.GetRecordValue[string](rec, "friend")
		since, _, _ := charta.GetRecordValue[int64](rec, "since")
		fmt.Printf("%s has known %s since %d\n", person, friend, since)
	}

	// Update: set a property.
	if _, err := db.RunQuery(ctx, `MATCH (p:Person {name: 'Alice'}) SET p.city = 'Leeds'`, nil); err != nil {
		log.Fatal(err)
	}

	// Several statements that must succeed or fail together run in a transaction.
	tx, err := db.BeginTx(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := tx.Run(ctx, `CREATE (:Person {name: 'Carol', born: 1992})`, nil); err != nil {
		_ = tx.Rollback()
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	count(ctx, db, "after adding Carol")

	// Remove: DETACH DELETE removes a node together with its relationships.
	res, err = db.RunQuery(ctx, `MATCH (p:Person {name: 'Bob'}) DETACH DELETE p`, nil)
	if err != nil {
		log.Fatal(err)
	}
	summary, _ = res.Consume(ctx)
	fmt.Printf("deleted %d node and %d relationship\n",
		summary.Counters().NodesDeleted(), summary.Counters().RelationshipsDeleted())
	count(ctx, db, "after removing Bob")
}

// count prints how many people the graph holds.
func count(ctx context.Context, db *charta.DB, when string) {
	res, err := db.RunQuery(ctx, `MATCH (p:Person) RETURN count(p) AS n`, nil)
	if err != nil {
		log.Fatal(err)
	}
	rec, err := res.Single(ctx)
	if err != nil {
		log.Fatal(err)
	}
	n, _, _ := charta.GetRecordValue[int64](rec, "n")
	fmt.Printf("%d people %s\n", n, when)
}
