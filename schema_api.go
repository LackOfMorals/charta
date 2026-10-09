package charta

import (
	"context"
	"fmt"
	"strings"
)

// SchemaInfo describes one index or constraint, as reported by SHOW INDEXES and
// SHOW CONSTRAINTS.
type SchemaInfo struct {
	// Name identifies the object; use it with DropIndex or DropConstraint.
	Name string
	// IsConstraint is true for a constraint, false for an index.
	IsConstraint bool
	// Type is the index type (RANGE, TEXT, POINT, LOOKUP, FULLTEXT, VECTOR) or
	// the constraint type (UNIQUENESS, NODE_KEY, NODE_PROPERTY_EXISTENCE,
	// NODE_PROPERTY_TYPE and their RELATIONSHIP_ counterparts).
	Type string
	// EntityType is "NODE" or "RELATIONSHIP".
	EntityType string
	// Labels are the labels (or relationship types) covered; empty for a lookup index.
	Labels []string
	// Properties are the indexed or constrained properties.
	Properties []string
	// OwningConstraint, for an index that backs a constraint, is its name.
	OwningConstraint string
}

// quoteIdent quotes s as a Cypher symbolic name, so that a label or property
// containing spaces, punctuation or backticks cannot change the statement.
func quoteIdent(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("charta: a schema name cannot be empty")
	}
	if strings.ContainsRune(s, 0) {
		return "", fmt.Errorf("charta: a schema name cannot contain a NUL character")
	}
	return "`" + strings.ReplaceAll(s, "`", "``") + "`", nil
}

func (d *DB) runSchema(ctx context.Context, cypher string) error {
	res, err := d.RunQuery(ctx, cypher, nil)
	if err != nil {
		return err
	}
	_, err = res.Consume(ctx)
	return err
}

// CreatePropertyIndex creates a range index on nodes with the given label for
// the given property, so equality lookups such as MATCH (n:Label {property: $v})
// use an index instead of scanning. It does nothing if an equivalent index
// already exists. It is the Go equivalent of
//
//	CREATE INDEX IF NOT EXISTS FOR (n:Label) ON (n.property)
//
// Indexes are also created automatically for properties that queries keep
// filtering on, once the graph is large enough to benefit.
func (d *DB) CreatePropertyIndex(ctx context.Context, label, property string) error {
	l, err := quoteIdent(label)
	if err != nil {
		return err
	}
	p, err := quoteIdent(property)
	if err != nil {
		return err
	}
	return d.runSchema(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS FOR (n:%s) ON (n.%s)", l, p))
}

// CreateUniqueConstraint makes the property unique among nodes with the given
// label (nodes without the property are ignored). A write that would create a
// duplicate fails with *ErrConstraintViolation and is rolled back, and the
// constraint cannot be created while existing data already has duplicates. It
// does nothing if the constraint already exists. It is the Go equivalent of
//
//	CREATE CONSTRAINT IF NOT EXISTS FOR (n:Label) REQUIRE n.property IS UNIQUE
func (d *DB) CreateUniqueConstraint(ctx context.Context, label, property string) error {
	l, err := quoteIdent(label)
	if err != nil {
		return err
	}
	p, err := quoteIdent(property)
	if err != nil {
		return err
	}
	return d.runSchema(ctx, fmt.Sprintf("CREATE CONSTRAINT IF NOT EXISTS FOR (n:%s) REQUIRE n.%s IS UNIQUE", l, p))
}

// DropIndex drops the index with the given name. It does nothing if there is
// no such index.
func (d *DB) DropIndex(ctx context.Context, name string) error {
	n, err := quoteIdent(name)
	if err != nil {
		return err
	}
	return d.runSchema(ctx, "DROP INDEX "+n+" IF EXISTS")
}

// DropConstraint drops the constraint with the given name, and the index that
// backs it. It does nothing if there is no such constraint.
func (d *DB) DropConstraint(ctx context.Context, name string) error {
	n, err := quoteIdent(name)
	if err != nil {
		return err
	}
	return d.runSchema(ctx, "DROP CONSTRAINT "+n+" IF EXISTS")
}

func stringList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ListSchema returns every index and constraint, in creation order (the two
// built-in token lookup indexes first).
func (d *DB) ListSchema(ctx context.Context) ([]SchemaInfo, error) {
	var out []SchemaInfo
	collect := func(cypher string, constraint bool) error {
		res, err := d.RunQuery(ctx, cypher, nil)
		if err != nil {
			return err
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			return err
		}
		for _, r := range recs {
			v := r.Values()
			info := SchemaInfo{IsConstraint: constraint}
			info.Name, _ = v[0].(string)
			info.Type, _ = v[1].(string)
			info.EntityType, _ = v[2].(string)
			info.Labels = stringList(v[3])
			info.Properties = stringList(v[4])
			if len(v) > 5 {
				info.OwningConstraint, _ = v[5].(string)
			}
			out = append(out, info)
		}
		return nil
	}
	if err := collect("SHOW INDEXES YIELD name, type, entityType, labelsOrTypes, properties, owningConstraint", false); err != nil {
		return nil, err
	}
	if err := collect("SHOW CONSTRAINTS YIELD name, type, entityType, labelsOrTypes, properties", true); err != nil {
		return nil, err
	}
	return out, nil
}
