// copy_to_neo4j demonstrates pushing a sub-graph from a local charta
// database into a remote Neo4j instance.
//
// Pattern: open the local charta file, query the sub-graph of interest
// using MATCH, then write each node and relationship into Neo4j using
// session.ExecuteWrite. This is useful for promoting a locally-built or
// test-generated graph to a shared Neo4j cluster.
//
// Environment variables:
//
//	CHARTA_PATH — source charta file path (default: graph.db)
//	NEO4J_URI      — bolt or neo4j URI (default: neo4j://localhost:7687)
//	NEO4J_USER     — username (default: neo4j)
//	NEO4J_PASS     — password (default: empty)
//
// Run with:
//
//	CHARTA_PATH=graph.db NEO4J_URI=neo4j://localhost:7687 NEO4J_USER=neo4j NEO4J_PASS=secret go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	charta "github.com/LackOfMorals/charta"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func main() {
	ctx := context.Background()

	// ── charta source ──────────────────────────────────────────────────────
	dbPath := envOrDefault("CHARTA_PATH", "graph.db")
	db, err := charta.Open(dbPath)
	if err != nil {
		log.Fatalf("charta.Open %q: %v", dbPath, err)
	}
	defer db.Close(ctx)

	// ── Neo4j destination ─────────────────────────────────────────────────────
	uri := envOrDefault("NEO4J_URI", "neo4j://localhost:7687")
	user := envOrDefault("NEO4J_USER", "neo4j")
	pass := envOrDefault("NEO4J_PASS", "")

	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, pass, ""))
	if err != nil {
		log.Fatalf("neo4j.NewDriverWithContext: %v", err)
	}
	defer driver.Close(ctx)

	// ── Read nodes from charta ─────────────────────────────────────────────
	nodeResult, err := db.RunQuery(ctx, `MATCH (n) RETURN n`, nil)
	if err != nil {
		log.Fatalf("charta MATCH nodes: %v", err)
	}
	nodeRecords, err := nodeResult.Collect(ctx)
	if err != nil {
		log.Fatalf("collect nodes: %v", err)
	}

	// ── Write nodes to Neo4j ──────────────────────────────────────────────────
	session := driver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	nodeCount := 0
	for _, rec := range nodeRecords {
		raw, ok := rec.Get("n")
		if !ok {
			continue
		}
		node, ok := raw.(*charta.Node)
		if !ok {
			continue
		}

		// Build the label string for the MERGE clause.
		labelStr := ""
		if len(node.Labels) > 0 {
			parts := make([]string, len(node.Labels))
			for i, l := range node.Labels {
				parts[i] = fmt.Sprintf("`%s`", l)
			}
			labelStr = ":" + strings.Join(parts, ":")
		}

		// Use MERGE on the charta ElementId to make the operation idempotent.
		cypher := fmt.Sprintf(
			"MERGE (n%s {_charta_id: $id}) SET n += $props",
			labelStr,
		)
		props := make(map[string]any, len(node.Props))
		for k, v := range node.Props {
			props[k] = v
		}
		params := map[string]any{
			"id":    node.ElementId,
			"props": props,
		}

		_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			return tx.Run(ctx, cypher, params)
		})
		if err != nil {
			log.Fatalf("neo4j write node %s: %v", node.ElementId, err)
		}
		nodeCount++
	}
	fmt.Printf("Pushed %d node(s) from charta → Neo4j\n", nodeCount)

	// ── Read relationships from charta ─────────────────────────────────────
	relResult, err := db.RunQuery(ctx, `MATCH (a)-[r]->(b) RETURN a, r, b`, nil)
	if err != nil {
		log.Fatalf("charta MATCH rels: %v", err)
	}
	relRecords, err := relResult.Collect(ctx)
	if err != nil {
		log.Fatalf("collect rels: %v", err)
	}

	// ── Write relationships to Neo4j ──────────────────────────────────────────
	relCount := 0
	for _, rec := range relRecords {
		rawRel, ok := rec.Get("r")
		if !ok {
			continue
		}
		rel, ok := rawRel.(*charta.Relationship)
		if !ok {
			continue
		}
		rawA, _ := rec.Get("a")
		rawB, _ := rec.Get("b")
		nodeA, _ := rawA.(*charta.Node)
		nodeB, _ := rawB.(*charta.Node)
		if nodeA == nil || nodeB == nil {
			continue
		}

		props := make(map[string]any, len(rel.Props))
		for k, v := range rel.Props {
			props[k] = v
		}
		params := map[string]any{
			"aid":   nodeA.ElementId,
			"bid":   nodeB.ElementId,
			"props": props,
		}
		cypher := fmt.Sprintf(
			"MATCH (a {_charta_id: $aid}), (b {_charta_id: $bid}) MERGE (a)-[r:`%s`]->(b) SET r += $props",
			rel.Type,
		)
		_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			return tx.Run(ctx, cypher, params)
		})
		if err != nil {
			log.Fatalf("neo4j write rel %s: %v", rel.ElementId, err)
		}
		relCount++
	}
	fmt.Printf("Pushed %d relationship(s) from charta → Neo4j\n", relCount)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
