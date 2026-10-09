package store

// schemaDDL is the complete DDL for the charta SQLite schema.
// It is executed once when a new database is opened or created.
//
// Design notes:
//   - labels is stored as comma-separated text in nodes.labels for compatibility.
//   - node_labels is a junction table with one row per (node_id, label) pair,
//     written together with the node (see InsertNode). It is the primary index for label lookups, replacing the LIKE-based scan.
//     The idx_node_labels_label index allows O(log n) lookups by label name.
//   - props is stored as a JSON column so that json_extract() can project and
//     filter individual property keys.
//   - All indexes use IF NOT EXISTS guards so this DDL is safe to re-run on
//     existing databases.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS nodes (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    labels TEXT    NOT NULL DEFAULT '',
    props  JSON    NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS edges (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT    NOT NULL,
    start_id INTEGER NOT NULL REFERENCES nodes(id),
    end_id   INTEGER NOT NULL REFERENCES nodes(id),
    props    JSON    NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS node_labels (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    label   TEXT    NOT NULL,
    UNIQUE (node_id, label)
);

CREATE INDEX IF NOT EXISTS idx_nodes_labels      ON nodes(labels);
CREATE INDEX IF NOT EXISTS idx_edges_start       ON edges(start_id);
CREATE INDEX IF NOT EXISTS idx_edges_end         ON edges(end_id);
CREATE INDEX IF NOT EXISTS idx_edges_type        ON edges(type);
CREATE INDEX IF NOT EXISTS idx_node_labels_label ON node_labels(label, node_id);

-- node_labels is maintained by the code that writes nodes (store.InsertNode and
-- the interpreter), not by triggers: a trigger firing for every inserted node
-- (even one that only inserted a single row) cost several times the node
-- insert itself. The triggers older databases may have are dropped.
DROP TRIGGER IF EXISTS trg_nodes_insert_label;
DROP TRIGGER IF EXISTS trg_nodes_insert_labels;
DROP TRIGGER IF EXISTS trg_nodes_update_label;
DROP TRIGGER IF EXISTS trg_nodes_update_labels;
`

// backfillMigrationSQL populates node_labels for any existing node rows that
// predate the junction table. It is run at Open time after schemaDDL.
// The WITH RECURSIVE CTE splits each row's comma-separated labels column.
//
// This operation is idempotent: the WHERE NOT EXISTS guard skips nodes that
// already have at least one row in node_labels (i.e. were populated by the
// triggers), and the UNIQUE(node_id, label) constraint combined with
// INSERT OR IGNORE prevents duplicate rows if the migration is re-run.
const backfillMigrationSQL = `
INSERT OR IGNORE INTO node_labels(node_id, label)
SELECT n.id, s.label
FROM nodes n
JOIN (
    WITH RECURSIVE split(node_id, labels_raw, label, rest) AS (
        SELECT id, labels,
               CASE WHEN INSTR(labels, ',') > 0
                    THEN SUBSTR(labels, 1, INSTR(labels, ',') - 1)
                    ELSE labels
               END,
               CASE WHEN INSTR(labels, ',') > 0
                    THEN SUBSTR(labels, INSTR(labels, ',') + 1)
                    ELSE ''
               END
        FROM nodes WHERE labels != ''
        UNION ALL
        SELECT node_id, labels_raw,
               CASE WHEN INSTR(rest, ',') > 0
                    THEN SUBSTR(rest, 1, INSTR(rest, ',') - 1)
                    ELSE rest
               END,
               CASE WHEN INSTR(rest, ',') > 0
                    THEN SUBSTR(rest, INSTR(rest, ',') + 1)
                    ELSE ''
               END
        FROM split WHERE rest != ''
    )
    SELECT node_id, label FROM split
) AS s ON s.node_id = n.id
WHERE NOT EXISTS (
    SELECT 1 FROM node_labels nl WHERE nl.node_id = n.id
);
`
