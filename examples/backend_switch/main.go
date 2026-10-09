// backend_switch demonstrates how to choose between a local charta database
// and a remote Neo4j instance at application startup based on an environment
// variable.
//
// Pattern: read CHARTA_BACKEND from the environment.
// - When CHARTA_BACKEND=local (default), open a charta in-memory database.
// - When CHARTA_BACKEND=neo4j, connect to Neo4j using NEO4J_URI / NEO4J_USER /
//   NEO4J_PASS environment variables.
//
// Both backends run the same MATCH query, demonstrating that application logic
// can be written once against charta's native API and switched to Neo4j for
// production without changing the query layer.
//
// Run with:
//
//	CHARTA_BACKEND=local go run .
//	CHARTA_BACKEND=neo4j NEO4J_URI=neo4j://localhost:7687 NEO4J_USER=neo4j NEO4J_PASS=secret go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	charta "github.com/LackOfMorals/charta"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func main() {
	ctx := context.Background()

	backend := os.Getenv("CHARTA_BACKEND")
	if backend == "" {
		backend = "local"
	}

	switch backend {
	case "local":
		runWithCharta(ctx)
	case "neo4j":
		runWithNeo4j(ctx)
	default:
		log.Fatalf("unknown CHARTA_BACKEND %q: expected \"local\" or \"neo4j\"", backend)
	}
}

// runWithCharta opens a local in-memory charta database, seeds it with a
// small graph, and runs a MATCH query using the charta native API.
func runWithCharta(ctx context.Context) {
	fmt.Println("Backend: charta (local)")

	db, err := charta.Open(":memory:")
	if err != nil {
		log.Fatalf("charta.Open: %v", err)
	}
	defer db.Close(ctx)

	// Seed with sample data.
	_, err = db.RunQuery(ctx, `CREATE (:Person {name: "Alice", age: 30})`, nil)
	if err != nil {
		log.Fatalf("seed: %v", err)
	}
	_, err = db.RunQuery(ctx, `CREATE (:Person {name: "Bob", age: 25})`, nil)
	if err != nil {
		log.Fatalf("seed: %v", err)
	}

	// Query people.
	result, err := db.RunQuery(ctx, `MATCH (p:Person) RETURN p.name AS name, p.age AS age`, nil)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	printResult(ctx, result)
}

// runWithNeo4j connects to a remote Neo4j instance using NEO4J_URI, NEO4J_USER,
// and NEO4J_PASS environment variables, then runs the same MATCH query.
func runWithNeo4j(ctx context.Context) {
	fmt.Println("Backend: Neo4j (remote)")

	uri := envOrDefault("NEO4J_URI", "neo4j://localhost:7687")
	user := envOrDefault("NEO4J_USER", "neo4j")
	pass := envOrDefault("NEO4J_PASS", "")

	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, pass, ""))
	if err != nil {
		log.Fatalf("neo4j.NewDriverWithContext: %v", err)
	}
	defer driver.Close(ctx)

	session := driver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	// Run the same query against Neo4j. The column names match the charta path.
	result, err := session.Run(ctx, `MATCH (p:Person) RETURN p.name AS name, p.age AS age`, nil)
	if err != nil {
		log.Fatalf("session.Run: %v", err)
	}
	for result.Next(ctx) {
		rec := result.Record()
		name, _ := rec.Get("name")
		age, _ := rec.Get("age")
		fmt.Printf("  name=%v  age=%v\n", name, age)
	}
	if err := result.Err(); err != nil {
		log.Fatalf("result iteration: %v", err)
	}
}

// printResult drains a charta *Result and prints each record's name and age columns.
func printResult(ctx context.Context, result *charta.Result) {
	for result.Next(ctx) {
		rec := result.Record()
		name, _ := rec.Get("name")
		age, _ := rec.Get("age")
		fmt.Printf("  name=%v  age=%v\n", name, age)
	}
	if _, err := result.Consume(ctx); err != nil {
		log.Fatalf("consume: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
