// Command movies builds the classic movies graph (Movie and Person nodes with
// ACTED_IN, DIRECTED, PRODUCED, WROTE, FOLLOWS and REVIEWED relationships) by
// reading a file of Cypher statements and running each one, then queries it.
//
//	go run ./examples/movies [file.cypher]
//
// The file defaults to movies.cypher next to this program's source, so run it
// from the repository root.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/LackOfMorals/charta"
)

func main() {
	file := "examples/movies/movies.cypher"
	if len(os.Args) > 1 {
		file = os.Args[1]
	}
	script, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	db, err := charta.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close(ctx)

	// Run the whole file in one transaction so a failure leaves nothing behind.
	tx, err := db.BeginTx(ctx)
	if err != nil {
		log.Fatal(err)
	}
	statements := splitStatements(string(script))
	var nodes, rels int
	for i, stmt := range statements {
		res, err := tx.Run(ctx, stmt, nil)
		if err != nil {
			_ = tx.Rollback()
			log.Fatalf("statement %d: %v\n%s", i+1, err, stmt)
		}
		summary, err := res.Consume(ctx)
		if err != nil {
			_ = tx.Rollback()
			log.Fatalf("statement %d: %v", i+1, err)
		}
		nodes += summary.Counters().NodesCreated()
		rels += summary.Counters().RelationshipsCreated()
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ran %d statements from %s: %d nodes and %d relationships created\n\n", len(statements), file, nodes, rels)

	query(ctx, db, "Tom Hanks's films", `
		MATCH (:Person {name: 'Tom Hanks'})-[r:ACTED_IN]->(m:Movie)
		RETURN m.title AS title, m.released AS released, r.roles AS roles
		ORDER BY released`)

	query(ctx, db, "People who worked with Keanu Reeves most often", `
		MATCH (:Person {name: 'Keanu Reeves'})-[:ACTED_IN]->(m:Movie)<-[:ACTED_IN]-(co:Person)
		RETURN co.name AS coActor, count(m) AS films
		ORDER BY films DESC, coActor LIMIT 5`)

	query(ctx, db, "Shortest path from Kevin Bacon to Meg Ryan", `
		MATCH p = shortestPath((:Person {name: 'Kevin Bacon'})-[:ACTED_IN*]-(:Person {name: 'Meg Ryan'}))
		RETURN [n IN nodes(p) | coalesce(n.name, n.title)] AS path`)

	query(ctx, db, "Top-rated movies", `
		MATCH (:Person)-[r:REVIEWED]->(m:Movie)
		RETURN m.title AS title, avg(r.rating) AS rating, count(r) AS reviews
		ORDER BY rating DESC`)
}

// query runs a read query and prints each row.
func query(ctx context.Context, db *charta.DB, title, cypher string) {
	fmt.Println(title)
	res, err := db.RunQuery(ctx, cypher, nil)
	if err != nil {
		log.Fatal(err)
	}
	for res.Next(ctx) {
		rec := res.Record()
		parts := make([]string, 0, len(rec.Keys()))
		for i, v := range rec.Values() {
			parts = append(parts, fmt.Sprintf("%s=%v", rec.Keys()[i], v))
		}
		fmt.Println("  " + strings.Join(parts, "  "))
	}
	fmt.Println()
}

// splitStatements splits a script at semicolons that are not inside a quoted
// string (honouring backslash escapes) and drops empty statements.
func splitStatements(script string) []string {
	var out []string
	var quote rune
	escaped := false
	start := 0
	flush := func(end int) {
		if s := strings.TrimSpace(script[start:end]); s != "" {
			out = append(out, s)
		}
	}
	for i, c := range script {
		switch {
		case escaped:
			escaped = false
		case quote != 0:
			if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ';':
			flush(i)
			start = i + 1
		}
	}
	flush(len(script))
	return out
}
