// Package proc describes callable procedures: their signatures and how call
// arguments are checked and coerced against them. It is shared by the semantic
// analysis (which validates calls before execution) and the interpreter (which
// runs them), and depends on nothing else in the module.
package proc

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Param is one named, typed procedure input or output.
type Param struct {
	Name string
	// Type is the Cypher type name in upper case: INTEGER, FLOAT, NUMBER,
	// STRING, BOOLEAN, MAP, LIST, NODE, RELATIONSHIP, PATH or ANY.
	Type string
	// Nullable is true if null is acceptable.
	Nullable bool
}

// Signature is a procedure's name and parameters.
type Signature struct {
	Name    string // dotted, e.g. "test.my.proc" (compared case-insensitively)
	Inputs  []Param
	Outputs []Param
}

// ParseType splits a type spec such as "INTEGER?" into its name and
// nullability.
func ParseType(spec string) (typ string, nullable bool) {
	spec = strings.TrimSpace(spec)
	nullable = strings.HasSuffix(spec, "?")
	return strings.ToUpper(strings.TrimSuffix(spec, "?")), nullable
}

// Coerce checks v against the parameter's type and converts it where the type
// allows (an integer given for FLOAT becomes a float). It returns an error
// message when v is not acceptable.
func Coerce(p Param, v any) (any, error) {
	if v == nil {
		if p.Nullable || p.Type == "ANY" {
			return nil, nil
		}
		return nil, fmt.Errorf("argument `%s` of type %s does not accept null", p.Name, p.Type)
	}
	switch p.Type {
	case "ANY", "":
		return v, nil
	case "INTEGER":
		if _, ok := v.(int64); ok {
			return v, nil
		}
	case "FLOAT":
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
	case "NUMBER":
		switch v.(type) {
		case int64, float64:
			return v, nil
		}
	case "STRING":
		if _, ok := v.(string); ok {
			return v, nil
		}
	case "BOOLEAN":
		if _, ok := v.(bool); ok {
			return v, nil
		}
	case "MAP":
		if _, ok := v.(map[string]any); ok {
			return v, nil
		}
	case "LIST":
		if _, ok := v.([]any); ok {
			return v, nil
		}
	default:
		return v, nil // entity types are not checked here
	}
	return nil, fmt.Errorf("argument `%s` expects %s", p.Name, p.Type)
}

// LiteralKind returns the Cypher type of a literal argument ("" if unknown),
// so calls can be rejected before execution.
func LiteralKind(v any) string {
	switch v.(type) {
	case int64:
		return "INTEGER"
	case float64:
		return "FLOAT"
	case string:
		return "STRING"
	case bool:
		return "BOOLEAN"
	case nil:
		return "NULL"
	case []any:
		return "LIST"
	case map[string]any:
		return "MAP"
	}
	return ""
}

// Accepts reports whether a literal of the given kind may be passed for p.
func (p Param) Accepts(kind string) bool {
	switch {
	case kind == "" || p.Type == "ANY" || p.Type == "":
		return true
	case kind == "NULL":
		return p.Nullable
	case p.Type == "NUMBER":
		return kind == "INTEGER" || kind == "FLOAT"
	case p.Type == "FLOAT":
		return kind == "INTEGER" || kind == "FLOAT"
	}
	return kind == p.Type
}

// Registry resolves procedures by name.
type Registry interface {
	Lookup(name string) (*Signature, bool)
}

// Builtin signatures of the procedures graphlite provides itself.
var Builtin = []*Signature{
	{Name: "db.labels", Outputs: []Param{{Name: "label", Type: "STRING"}}},
	{Name: "db.relationshipTypes", Outputs: []Param{{Name: "relationshipType", Type: "STRING"}}},
	{Name: "db.propertyKeys", Outputs: []Param{{Name: "propertyKey", Type: "STRING"}}},
}

// Key normalises a procedure name for lookup.
func Key(name string) string { return strings.ToLower(name) }

// Func runs a procedure. It receives the coerced arguments in declaration order
// and returns one map per output row, keyed by output name.
type Func func(ctx context.Context, args []any) ([]map[string]any, error)

// Procedure is a signature together with its implementation.
type Procedure struct {
	Signature
	Fn Func
}

// Set is a concurrency-safe collection of procedures. It implements Registry.
type Set struct {
	mu sync.RWMutex
	m  map[string]*Procedure
}

// Register adds or replaces a procedure.
func (s *Set) Register(p *Procedure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*Procedure{}
	}
	s.m[Key(p.Name)] = p
}

// Clear removes every registered procedure.
func (s *Set) Clear() {
	s.mu.Lock()
	s.m = nil
	s.mu.Unlock()
}

// Get returns the procedure registered under name.
func (s *Set) Get(name string) (*Procedure, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.m[Key(name)]
	return p, ok
}

// Lookup implements Registry.
func (s *Set) Lookup(name string) (*Signature, bool) {
	p, ok := s.Get(name)
	if !ok {
		return nil, false
	}
	return &p.Signature, true
}
