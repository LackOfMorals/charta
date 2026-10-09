// Command csvImport loads a large flat CSV (an API request log, one row per
// request) into a charta graph with the bulk CSV importer, and reports how long
// each step takes.
//
// The log is not in the node/relationship layout the importer reads, so the
// program first converts it (-prepare) into node and relationship files:
//
//	(:Client {ip, region})-[:MADE]->(:Request {timestamp, status, method, ...})
//	(:Request)-[:TARGETS]->(:Instance {id})
//	(:Request)-[:USING]->(:UserAgent {name})
//
// and then imports those files (-load) and runs a few queries against the result.
//
//	go run ./examples/csvImport -prepare -load            # import.csv in this directory
//	go run ./examples/csvImport -load -rows 200000        # a quick run on the first 200k rows
//
// The importer's node ids are the database's own sequential ids, so the files
// are written (and must be imported) in order into an empty database:
// clients, instances, user agents, then requests, then relationships.
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/LackOfMorals/charta"
)

// Columns of import.csv (the header repeats request_method; the first is used).
const (
	colTimestamp = 0
	colStatus    = 1
	colProtocol  = 2
	colMethod    = 3
	colURL       = 4
	colInstance  = 6
	colUserAgent = 7
	colIP        = 8
	colRegion    = 9
	colAction    = 10
	colOutcome   = 11
	colRateLimit = 12
	numCols      = 13
)

func main() {
	dir := "examples/csvImport"
	in := flag.String("in", filepath.Join(dir, "import.csv"), "source CSV")
	out := flag.String("out", filepath.Join(dir, "out"), "directory for the generated node/relationship files")
	dbPath := flag.String("db", "", "database file (default: a temporary file, removed afterwards; \":memory:\" for in-memory)")
	prepare := flag.Bool("prepare", false, "convert -in into node/relationship files in -out")
	load := flag.Bool("load", false, "import the files in -out and run the queries")
	rows := flag.Int("rows", 0, "limit to the first N data rows (0 = all)")
	chunk := flag.Int("chunk", 500_000, "requests per node file (the importer reads a whole file into memory and accepts at most 500 MiB)")
	flag.Parse()
	if !*prepare && !*load {
		flag.Usage()
		os.Exit(2)
	}
	if *prepare {
		if err := prepareFiles(*in, *out, *rows, *chunk); err != nil {
			log.Fatal(err)
		}
	}
	if *load {
		if err := loadFiles(*out, *dbPath); err != nil {
			log.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Conversion
// ---------------------------------------------------------------------------

func openCSV(path string) (*csv.Reader, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.FieldsPerRecord = -1 // tolerate ragged rows; they are skipped below
	r.ReuseRecord = true
	r.LazyQuotes = true
	if _, err := r.Read(); err != nil { // header
		f.Close()
		return nil, nil, err
	}
	return r, f, nil
}

// next returns the next well-formed data row, or io.EOF.
func next(r *csv.Reader, skipped *int) ([]string, error) {
	for {
		rec, err := r.Read()
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				*skipped++
				continue
			}
			return nil, err
		}
		if len(rec) < numCols {
			*skipped++
			continue
		}
		return rec, nil
	}
}

type entities struct {
	ids  map[string]int64 // key -> node id
	keys []string         // in id order
	prop map[string]string
}

func newEntities() *entities {
	return &entities{ids: map[string]int64{}, prop: map[string]string{}}
}

func (e *entities) add(key, prop string) {
	if _, ok := e.ids[key]; ok {
		return
	}
	e.ids[key] = int64(len(e.keys)) + 1 // provisional (0-based offset applied later)
	e.keys = append(e.keys, key)
	e.prop[key] = prop
}

func prepareFiles(in, out string, limit, chunk int) error {
	start := time.Now()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	// Pass 1: the distinct clients, instances and user agents, and the row count.
	r, f, err := openCSV(in)
	if err != nil {
		return err
	}
	clients, instances, agents := newEntities(), newEntities(), newEntities()
	total, skipped := 0, 0
	for limit == 0 || total < limit {
		rec, err := next(r, &skipped)
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return err
		}
		clients.add(rec[colIP], rec[colRegion])
		instances.add(rec[colInstance], "")
		agents.add(rec[colUserAgent], "")
		total++
	}
	f.Close()

	// Entity ids come first: clients, then instances, then user agents, then requests.
	nc, ni, na := int64(len(clients.keys)), int64(len(instances.keys)), int64(len(agents.keys))
	clientBase, instBase, agentBase, reqBase := int64(0), nc, nc+ni, nc+ni+na
	fmt.Printf("pass 1: %d requests (%d malformed rows skipped), %d clients, %d instances, %d user agents  [%s]\n",
		total, skipped, nc, ni, na, time.Since(start).Round(time.Millisecond))

	if err := writeEntities(filepath.Join(out, "nodes_1_clients.csv"), "Client", clients); err != nil {
		return err
	}
	if err := writeEntities(filepath.Join(out, "nodes_2_instances.csv"), "Instance", instances); err != nil {
		return err
	}
	if err := writeEntities(filepath.Join(out, "nodes_3_useragents.csv"), "UserAgent", agents); err != nil {
		return err
	}

	// Pass 2: request nodes and the relationships, in chunks.
	r, f, err = openCSV(in)
	if err != nil {
		return err
	}
	defer f.Close()
	var nodesW, edgesW *csv.Writer
	var nodesF, edgesF *os.File
	closeChunk := func() error {
		var firstErr error
		for _, w := range []*csv.Writer{nodesW, edgesW} {
			if w != nil {
				w.Flush()
				if err := w.Error(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		for _, f := range []*os.File{nodesF, edgesF} {
			if f != nil {
				if err := f.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		nodesW, edgesW, nodesF, edgesF = nil, nil, nil, nil
		return firstErr
	}
	openChunk := func(n int) error {
		var err error
		if nodesF, err = os.Create(filepath.Join(out, fmt.Sprintf("nodes_4_requests_%04d.csv", n))); err != nil {
			return err
		}
		if edgesF, err = os.Create(filepath.Join(out, fmt.Sprintf("edges_%04d.csv", n))); err != nil {
			return err
		}
		nodesW = csv.NewWriter(bufio.NewWriterSize(nodesF, 1<<20))
		edgesW = csv.NewWriter(bufio.NewWriterSize(edgesF, 1<<20))
		if err := nodesW.Write([]string{":ID", ":LABEL", "timestamp:string", "status:int", "protocol:string", "method:string",
			"url:string", "configured_action:string", "outcome:string", "rate_limit_outcome:string"}); err != nil {
			return err
		}
		return edgesW.Write([]string{":START_ID", ":END_ID", ":TYPE"})
	}
	skipped = 0
	id := reqBase
	chunkNo := 0
	for n := 0; limit == 0 || n < limit; n++ {
		rec, err := next(r, &skipped)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if n%chunk == 0 {
			if err := closeChunk(); err != nil {
				return err
			}
			chunkNo++
			if err := openChunk(chunkNo); err != nil {
				return err
			}
		}
		id++
		status := rec[colStatus]
		if _, err := strconv.Atoi(status); err != nil {
			status = ""
		}
		_ = nodesW.Write([]string{strconv.FormatInt(id, 10), "Request", rec[colTimestamp], status, rec[colProtocol], rec[colMethod],
			rec[colURL], rec[colAction], rec[colOutcome], rec[colRateLimit]})
		sid := strconv.FormatInt(id, 10)
		_ = edgesW.Write([]string{strconv.FormatInt(clientBase+clients.ids[rec[colIP]], 10), sid, "MADE"})
		_ = edgesW.Write([]string{sid, strconv.FormatInt(instBase+instances.ids[rec[colInstance]], 10), "TARGETS"})
		_ = edgesW.Write([]string{sid, strconv.FormatInt(agentBase+agents.ids[rec[colUserAgent]], 10), "USING"})
	}
	if err := closeChunk(); err != nil {
		return err
	}
	fmt.Printf("prepare: wrote %d request files and %d relationships to %s in %s\n\n",
		chunkNo, 3*total, out, time.Since(start).Round(time.Millisecond))
	return nil
}

// writeEntities writes one node file whose ids are 1..N in key order.
func writeEntities(path, label string, e *entities) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(bufio.NewWriter(f))
	header := []string{":ID", ":LABEL"}
	if label == "Client" {
		header = append(header, "ip:string", "region:string")
	} else if label == "Instance" {
		header = append(header, "id:string")
	} else {
		header = append(header, "name:string")
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for i, k := range e.keys {
		row := []string{strconv.Itoa(i + 1), label, k}
		if label == "Client" {
			row = append(row, e.prop[k])
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// ---------------------------------------------------------------------------
// Import and queries
// ---------------------------------------------------------------------------

func loadFiles(dir, dbPath string) error {
	ctx := context.Background()
	if dbPath == "" {
		tmp, err := os.MkdirTemp("", "charta-csvimport-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		dbPath = filepath.Join(tmp, "graph.db")
	}
	db, err := charta.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() { // sample the heap while importing
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
			}
		}
	}()

	files, err := filepath.Glob(filepath.Join(dir, "nodes_*.csv"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	edgeFiles, _ := filepath.Glob(filepath.Join(dir, "edges_*.csv"))
	sort.Strings(edgeFiles)
	if len(files) == 0 {
		return fmt.Errorf("no node files in %s: run with -prepare first", dir)
	}

	fmt.Printf("%-34s %10s %12s\n", "import", "time", "MiB/s")
	var totalBytes int64
	begin := time.Now()
	importAll := func(paths []string, format charta.Format) error {
		for _, p := range paths {
			st, err := os.Stat(p)
			if err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			t := time.Now()
			err = db.Import(ctx, bufio.NewReaderSize(f, 1<<20), format)
			f.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(p), err)
			}
			d := time.Since(t)
			totalBytes += st.Size()
			fmt.Printf("%-34s %10s %12.1f\n", filepath.Base(p), d.Round(time.Millisecond), float64(st.Size())/(1<<20)/d.Seconds())
		}
		return nil
	}
	if err := importAll(files, charta.FormatCSVNodes); err != nil {
		return err
	}
	if err := importAll(edgeFiles, charta.FormatCSVEdges); err != nil {
		return err
	}
	elapsed := time.Since(begin)
	close(stop)
	var nodes, rels int64
	nodes = scalar(ctx, db, "MATCH (n) RETURN count(n)")
	rels = scalar(ctx, db, "MATCH ()-[r]->() RETURN count(r)")
	fmt.Printf("\nimported %d nodes and %d relationships (%.0f MiB of CSV) in %s: %.0f nodes+rels/s, peak heap %d MiB\n\n",
		nodes, rels, float64(totalBytes)/(1<<20), elapsed.Round(time.Millisecond),
		float64(nodes+rels)/elapsed.Seconds(), peak.Load()>>20)

	fmt.Printf("%-52s %10s  %s\n", "query", "time", "first row")
	for _, q := range []struct{ name, cypher string }{
		{"count requests by status", `MATCH (r:Request) RETURN r.status AS status, count(*) AS n ORDER BY n DESC LIMIT 5`},
		{"busiest clients", `MATCH (c:Client)-[:MADE]->(r:Request) RETURN c.ip AS ip, count(r) AS requests ORDER BY requests DESC LIMIT 5`},
		{"requests per user agent", `MATCH (r:Request)-[:USING]->(u:UserAgent) RETURN u.name AS agent, count(r) AS n ORDER BY n DESC LIMIT 5`},
		{"look up one client's requests (by ip)", `MATCH (c:Client {ip: '54.224.4.41'})-[:MADE]->(r:Request) RETURN count(r) AS n`},
		{"requests to one instance, by status", `MATCH (r:Request)-[:TARGETS]->(:Instance {id: 'ba6aa96d'}) RETURN r.status AS status, count(*) AS n ORDER BY n DESC`},
		{"clients that hit the same instances as one client", `MATCH (:Client {ip: '35.198.196.208'})-[:MADE]->(:Request)-[:TARGETS]->(i:Instance) WITH DISTINCT i MATCH (i)<-[:TARGETS]-(:Request)<-[:MADE]-(o:Client) RETURN count(DISTINCT o) AS clients`},
		{"error requests (status >= 500) per region", `MATCH (c:Client)-[:MADE]->(r:Request) WHERE r.status >= 500 RETURN c.region AS region, count(r) AS n ORDER BY n DESC LIMIT 5`},
	} {
		t := time.Now()
		res, err := db.RunQuery(ctx, q.cypher, nil)
		var first string
		if err == nil {
			var recs []*charta.Record
			if recs, err = res.Collect(ctx); err == nil && len(recs) > 0 {
				first = fmt.Sprint(recs[0].Values())
			}
		}
		if err != nil {
			first = "error: " + err.Error()
		}
		fmt.Printf("%-52s %10s  %s\n", q.name, time.Since(t).Round(time.Millisecond), first)
	}
	if st, err := os.Stat(dbPath); err == nil {
		fmt.Printf("\ndatabase file: %d MiB\n", st.Size()>>20)
	}
	return nil
}

// scalar returns the single integer a one-row query yields.
func scalar(ctx context.Context, db *charta.DB, q string) int64 {
	res, err := db.RunQuery(ctx, q, nil)
	if err != nil {
		return -1
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return -1
	}
	n, _ := rec.Values()[0].(int64)
	return n
}
