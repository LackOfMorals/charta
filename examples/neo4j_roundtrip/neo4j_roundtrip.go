//go:build ignore

// neo4j_roundtrip shows the pull → modify → push cycle using charta as a
// local processing layer between two drivers.
//
//  1. Pull: use CopyFrom to copy a graph from a remote Neo4j driver into a
//     local in-memory charta instance.
//  2. Modify: run Cypher against the local copy to enrich or transform the data.
//  3. Push: use CopyTo to promote the modified graph to the destination driver.
//
// The remote and destination variables below use in-memory charta instances
// so this example runs without a live Neo4j server. To point at real Neo4j,
// replace those two lines with:
//
//	import (
//	    "github.com/neo4j/neo4j-go-driver/v6/neo4j"
//	    "github.com/LackOfMorals/charta/neo4jadapter"
//	)
//
//	neo4jDriver, err := neo4j.NewDriverWithContext(
//	    "neo4j+s://xxx.databases.neo4j.io",
//	    neo4j.BasicAuth("neo4j", "password", ""),
//	)
//	remote := neo4jadapter.New(neo4jDriver)
//
// Run with:
//
//	go run github.com/LackOfMorals/charta/examples/neo4j_roundtrip.go
//
// or from the repo root:
//
//	go run examples/neo4j_roundtrip.go
package main

import (
	"context"
	"fmt"

	charta "github.com/LackOfMorals/charta"
)

func main() {
	ctx := context.Background()

	// ── Step 1: seed the remote source ───────────────────────────────────────
	// Using an in-memory charta instance here. In production replace with:
	//   remote := neo4jadapter.New(neo4jDriver)
	remote, err := charta.NewDriver(":memory:", charta.NoAuth())
	must(err)
	defer remote.Close(ctx)

	seed(ctx, remote)
	fmt.Println("=== Remote graph (before) ===")
	printGraph(ctx, remote)

	// ── Step 2: pull into a local charta instance ──────────────────────────
	local, err := charta.Open(":memory:")
	must(err)
	defer local.Close(ctx)

	must(local.CopyFrom(ctx, remote))

	// ── Step 3: modify the local copy ────────────────────────────────────────
	// Tag each department with the region it belongs to.
	for name, region := range map[string]string{
		"Engineering": "EMEA",
		"Sales":       "AMER",
		"Support":     "APAC",
	} {
		_, err = local.RunQuery(ctx,
			`MATCH (d:Department {name: $name}) SET d.region = $region`,
			map[string]any{"name": name, "region": region},
		)
		must(err)
	}

	// Promote engineers with seniority > 5 years to "Senior Engineer".
	for _, name := range []string{"Alice", "Bob"} {
		qr, err := local.RunQuery(ctx,
			`MATCH (e:Employee {name: $name}) RETURN e.yearsExp AS yrs`,
			map[string]any{"name": name},
		)
		must(err)
		eager, err := charta.NewEagerResult(ctx, qr)
		must(err)
		if len(eager.Records) == 0 {
			continue
		}
		yrs, _ := eager.Records[0].Get("yrs")
		if toFloat(yrs) > 5 {
			_, err = local.RunQuery(ctx,
				`MATCH (e:Employee {name: $name}) SET e.role = "Senior Engineer"`,
				map[string]any{"name": name},
			)
			must(err)
		}
	}

	// Link employees to their manager where one was not set remotely.
	_, err = local.RunQuery(ctx,
		`MATCH (mgr:Employee {name: "Alice"}), (rep:Employee {name: "Carol"})
		 CREATE (mgr)-[:MANAGES]->(rep)`,
		nil,
	)
	must(err)

	// ── Step 4: push the enriched graph to the destination ───────────────────
	// Using an in-memory charta instance here. In production replace with:
	//   destination := neo4jadapter.New(destinationNeo4jDriver)
	destination, err := charta.NewDriver(":memory:", charta.NoAuth())
	must(err)
	defer destination.Close(ctx)

	must(local.CopyTo(ctx, destination))

	fmt.Println("\n=== Destination graph (after enrichment) ===")
	printGraph(ctx, destination)
	printManagers(ctx, destination)
}

// seed populates the driver with a small org-chart graph.
func seed(ctx context.Context, driver charta.Driver) {
	queries := []struct {
		cypher string
		params map[string]any
	}{
		{`CREATE (:Department {name: "Engineering"})`, nil},
		{`CREATE (:Department {name: "Sales"})`, nil},
		{`CREATE (:Department {name: "Support"})`, nil},
		{`CREATE (:Employee {name: "Alice", role: "Engineer", yearsExp: 8})`, nil},
		{`CREATE (:Employee {name: "Bob", role: "Engineer", yearsExp: 3})`, nil},
		{`CREATE (:Employee {name: "Carol", role: "Sales Rep", yearsExp: 6})`, nil},
		{
			`MATCH (e:Employee {name: "Alice"}), (d:Department {name: "Engineering"})
			 CREATE (e)-[:WORKS_IN]->(d)`,
			nil,
		},
		{
			`MATCH (e:Employee {name: "Bob"}), (d:Department {name: "Engineering"})
			 CREATE (e)-[:WORKS_IN]->(d)`,
			nil,
		},
		{
			`MATCH (e:Employee {name: "Carol"}), (d:Department {name: "Sales"})
			 CREATE (e)-[:WORKS_IN]->(d)`,
			nil,
		},
	}
	for _, q := range queries {
		_, err := charta.ExecuteQuery[*charta.EagerResult](ctx, driver, q.cypher, q.params, charta.EagerResultTransformer)
		must(err)
	}
}

// printGraph prints employees, their roles, and the department they work in.
func printGraph(ctx context.Context, driver charta.Driver) {
	result, err := charta.ExecuteQuery[*charta.EagerResult](ctx, driver,
		`MATCH (e:Employee)-[:WORKS_IN]->(d:Department)
		 RETURN e.name AS name, e.role AS role, e.yearsExp AS exp,
		        d.name AS dept, d.region AS region
		 ORDER BY e.name`,
		nil, charta.EagerResultTransformer,
	)
	must(err)
	for _, rec := range result.Records {
		m := rec.AsMap()
		fmt.Printf("  %-8s  role=%-18s exp=%-3v dept=%-14s region=%v\n",
			m["name"], m["role"], m["exp"], m["dept"], m["region"])
	}
}

// printManagers prints MANAGES relationships.
func printManagers(ctx context.Context, driver charta.Driver) {
	result, err := charta.ExecuteQuery[*charta.EagerResult](ctx, driver,
		`MATCH (mgr:Employee)-[:MANAGES]->(rep:Employee)
		 RETURN mgr.name AS manager, rep.name AS report`,
		nil, charta.EagerResultTransformer,
	)
	must(err)
	for _, rec := range result.Records {
		m := rec.AsMap()
		fmt.Printf("  %s manages %s\n", m["manager"], m["report"])
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	return 0
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
