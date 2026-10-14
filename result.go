package graingraph

import "context"

// ─────────────────────────────────────────────────────────────────────────────
// Result — lazy streaming result cursor
// ─────────────────────────────────────────────────────────────────────────────

// Result is a cursor over the records of a query result.
// Call Next to advance the cursor, Record to read the current row, and
// Err to check for iteration errors. Always call Consume or allow the
// iteration to exhaust the result to release underlying resources.
type Result struct {
	keys     []string
	record   *Record
	err      error
	consumed bool
	counters queryCounters

	// inMemory holds the result's records; Next/Record/Consume iterate over it.
	inMemory    []*Record
	inMemoryPos int
}

// newInMemoryResult constructs a Result over a pre-collected slice of records.
func newInMemoryResult(keys []string, records []*Record) *Result {
	if records == nil {
		records = []*Record{}
	}
	return &Result{
		keys:     keys,
		inMemory: records,
	}
}

// Keys returns the projection key names for this result set.
func (r *Result) Keys() []string {
	out := make([]string, len(r.keys))
	copy(out, r.keys)
	return out
}

// Next advances the cursor to the next record. Returns true if a record is
// available; false when the result set is exhausted or an error occurred.
// If the context is already cancelled or has timed out, Next immediately
// returns false and sets Err to ctx.Err().
func (r *Result) Next(ctx context.Context) bool {
	if err := ctx.Err(); err != nil {
		r.err = err
		r.consumed = true
		return false
	}
	if r.consumed || r.err != nil {
		return false
	}
	if r.inMemoryPos >= len(r.inMemory) {
		r.consumed = true
		return false
	}
	r.record = r.inMemory[r.inMemoryPos]
	r.inMemoryPos++
	return true
}

// Record returns the current record. Returns nil if Next has not been called
// or if the cursor is exhausted.
func (r *Result) Record() *Record {
	return r.record
}

// Err returns the first error encountered during iteration.
func (r *Result) Err() error {
	return r.err
}

// Consume drains any remaining records, closes the underlying *sql.Rows, and
// returns the ResultSummary. After Consume returns the cursor is closed.
// Consume is safe to call on a write result (where rows is nil) and on
// in-memory results.
func (r *Result) Consume(_ context.Context) (ResultSummary, error) {
	r.consumed = true
	return &resultSummary{counters: r.counters}, r.err
}

// Collect drains all remaining records into a slice and closes the cursor.
func (r *Result) Collect(_ context.Context) ([]*Record, error) {
	recs := r.inMemory[r.inMemoryPos:]
	r.inMemoryPos = len(r.inMemory)
	r.consumed = true
	if r.err != nil {
		return nil, r.err
	}
	return recs, nil
}

// Single returns the one and only record from the result set. It is a
// convenience method for queries expected to return exactly one row.
//
//   - If the result set is empty, Single returns (nil, ErrNoRecords).
//   - If the result set has exactly one record, Single returns that record and nil.
//   - If the result set has two or more records, Single drains the cursor and
//     returns (nil, ErrMultipleRecords).
//
// Single always closes the cursor before returning.
func (r *Result) Single(ctx context.Context) (*Record, error) {
	if !r.Next(ctx) {
		// Drain and close.
		_, _ = r.Consume(ctx)
		if r.err != nil {
			return nil, r.err
		}
		return nil, ErrNoRecords
	}
	rec := r.Record()

	// Check whether a second record exists.
	if r.Next(ctx) {
		// Drain remaining records before returning. Any drain/close error is
		// secondary to ErrMultipleRecords, which is the primary signal here.
		_, _ = r.Consume(ctx)
		return nil, ErrMultipleRecords
	}

	// Exactly one record — close the cursor cleanly.
	_, _ = r.Consume(ctx)
	if r.err != nil {
		return nil, r.err
	}
	return rec, nil
}

// setCounters attaches write-operation counters to this result. It is called
// by the execution layer after executing write statements.
func (r *Result) setCounters(c queryCounters) {
	r.counters = c
}

// ─────────────────────────────────────────────────────────────────────────────
// ResultSummary and Counters
// ─────────────────────────────────────────────────────────────────────────────

// queryCounters accumulates write-operation statistics for a single query.
type queryCounters struct {
	nodesCreated         int
	nodesDeleted         int
	relationshipsCreated int
	relationshipsDeleted int
	propertiesSet        int
	propertiesRemoved    int
	labelsAdded          int
	labelsRemoved        int
	nodeLabelsAdded      int
	nodeLabelsRemoved    int
	events               Statistics // Neo4j-style event counts
	indexesAdded         int
	indexesRemoved       int
	constraintsAdded     int
	constraintsRemoved   int
}

// ResultSummary reports execution statistics and metadata for a completed query.
type ResultSummary interface {
	// Counters returns statistics about graph mutations performed by the query.
	// They compare the graph before and after the statement (the openCypher
	// TCK's side effects), so a node created and deleted in one statement
	// leaves no trace.
	Counters() Counters
	// Statistics returns the same mutations counted the way a Neo4j server
	// reports them: each one as it happens. Use it to give clients of a
	// Neo4j-compatible service the numbers Neo4j would.
	Statistics() Statistics
}

// Statistics counts a statement's side effects as Neo4j's query statistics do:
// one count per change, at the moment it happens, not netted over the
// statement. The differences from Counters:
//
//   - a node or relationship created and deleted in the same statement counts
//     as both;
//   - PropertiesSet counts every assignment, including setting a property to
//     the value it already has and removing an existing property;
//   - LabelsAdded and LabelsRemoved count one per node and label (the same
//     numbers as Counters.NodeLabelsAdded and NodeLabelsRemoved);
//   - deleting a node or relationship counts neither its properties nor its
//     labels.
type Statistics struct {
	NodesCreated         int
	NodesDeleted         int
	RelationshipsCreated int
	RelationshipsDeleted int
	PropertiesSet        int
	LabelsAdded          int
	LabelsRemoved        int
	IndexesAdded         int
	IndexesRemoved       int
	ConstraintsAdded     int
	ConstraintsRemoved   int
}

// ContainsUpdates reports whether the statement changed anything: the
// Neo4j containsUpdates flag.
func (s Statistics) ContainsUpdates() bool {
	return s.NodesCreated > 0 || s.NodesDeleted > 0 ||
		s.RelationshipsCreated > 0 || s.RelationshipsDeleted > 0 ||
		s.PropertiesSet > 0 || s.LabelsAdded > 0 || s.LabelsRemoved > 0 ||
		s.IndexesAdded > 0 || s.IndexesRemoved > 0 ||
		s.ConstraintsAdded > 0 || s.ConstraintsRemoved > 0
}

// Counters reports the number of graph mutations performed by a query.
type Counters interface {
	// NodesCreated returns the number of nodes created.
	NodesCreated() int
	// NodesDeleted returns the number of nodes deleted.
	NodesDeleted() int
	// RelationshipsCreated returns the number of relationships created.
	RelationshipsCreated() int
	// RelationshipsDeleted returns the number of relationships deleted.
	RelationshipsDeleted() int
	// PropertiesSet returns the number of property values written.
	PropertiesSet() int
	// PropertiesRemoved returns the number of property values removed.
	PropertiesRemoved() int
	// LabelsAdded returns the number of labels that first appeared in the
	// graph during the statement (a label already on any node does not count),
	// the meaning the openCypher TCK gives "+labels".
	LabelsAdded() int
	// LabelsRemoved returns the number of labels that no longer appear on any
	// node after the statement (the TCK's "-labels").
	LabelsRemoved() int
	// NodeLabelsAdded returns the number of labels added to nodes, one per
	// node and label, counted when the label is added. This is what Neo4j
	// reports as labelsAdded: CREATE (:A), (:A) adds two, and SET n:A on a
	// node that already has A adds none.
	NodeLabelsAdded() int
	// NodeLabelsRemoved returns the number of labels removed from nodes, one
	// per node and label. Deleting a node does not count, as in Neo4j.
	NodeLabelsRemoved() int
	// IndexesAdded and IndexesRemoved count schema indexes created and dropped.
	IndexesAdded() int
	IndexesRemoved() int
	// ConstraintsAdded and ConstraintsRemoved count schema constraints created and dropped.
	ConstraintsAdded() int
	ConstraintsRemoved() int
	// ContainsUpdates returns true when any mutation counter is greater than zero.
	ContainsUpdates() bool
}

// resultSummary is the concrete implementation of ResultSummary.
type resultSummary struct {
	counters queryCounters
}

// Counters implements ResultSummary.
func (s *resultSummary) Counters() Counters {
	return &counters{c: s.counters}
}

// Statistics implements ResultSummary.
func (s *resultSummary) Statistics() Statistics {
	return s.counters.events
}

// counters is the concrete implementation of Counters.
type counters struct {
	c queryCounters
}

func (c *counters) NodesCreated() int         { return c.c.nodesCreated }
func (c *counters) NodesDeleted() int         { return c.c.nodesDeleted }
func (c *counters) RelationshipsCreated() int { return c.c.relationshipsCreated }
func (c *counters) RelationshipsDeleted() int { return c.c.relationshipsDeleted }
func (c *counters) PropertiesSet() int        { return c.c.propertiesSet }
func (c *counters) PropertiesRemoved() int    { return c.c.propertiesRemoved }
func (c *counters) LabelsAdded() int          { return c.c.labelsAdded }
func (c *counters) LabelsRemoved() int        { return c.c.labelsRemoved }
func (c *counters) NodeLabelsAdded() int      { return c.c.nodeLabelsAdded }
func (c *counters) NodeLabelsRemoved() int    { return c.c.nodeLabelsRemoved }
func (c *counters) IndexesAdded() int         { return c.c.indexesAdded }
func (c *counters) IndexesRemoved() int       { return c.c.indexesRemoved }
func (c *counters) ConstraintsAdded() int     { return c.c.constraintsAdded }
func (c *counters) ConstraintsRemoved() int   { return c.c.constraintsRemoved }
func (c *counters) ContainsUpdates() bool {
	return c.c.nodesCreated > 0 || c.c.nodesDeleted > 0 ||
		c.c.relationshipsCreated > 0 || c.c.relationshipsDeleted > 0 ||
		c.c.propertiesSet > 0 || c.c.propertiesRemoved > 0 ||
		c.c.labelsAdded > 0 || c.c.labelsRemoved > 0 ||
		c.c.nodeLabelsAdded > 0 || c.c.nodeLabelsRemoved > 0 ||
		c.c.indexesAdded > 0 || c.c.indexesRemoved > 0 || c.c.constraintsAdded > 0 || c.c.constraintsRemoved > 0
}
