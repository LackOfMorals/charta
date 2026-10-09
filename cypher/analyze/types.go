package analyze

import "github.com/LackOfMorals/charta/cypher/syntax"

// kind is a coarse static type. kAny means "unknown", and an unknown type never
// causes an error: the analysis only reports what it can prove.
type kind uint8

const (
	kAny kind = iota
	kNull
	kBool
	kInt
	kFloat
	kString
	kList
	kMap
	kNode
	kRel
	kPath
)

type typ struct {
	k    kind
	elem *typ // element type of a kList; nil when unknown
}

var (
	tAny    = typ{}
	tNull   = typ{k: kNull}
	tBool   = typ{k: kBool}
	tInt    = typ{k: kInt}
	tFloat  = typ{k: kFloat}
	tString = typ{k: kString}
	tMap    = typ{k: kMap}
	tNode   = typ{k: kNode}
	tRel    = typ{k: kRel}
	tPath   = typ{k: kPath}
)

func listOf(elem typ) typ {
	if elem.k == kAny {
		return typ{k: kList}
	}
	return typ{k: kList, elem: &elem}
}

func (t typ) elemType() typ {
	if t.k == kList && t.elem != nil {
		return *t.elem
	}
	return tAny
}

// known reports whether the type is definite (not unknown and not null).
func (t typ) known() bool { return t.k != kAny && t.k != kNull }

func (t typ) numeric() bool { return t.k == kInt || t.k == kFloat }

// acceptable reports whether t may be used where a value of any of the given
// kinds is expected: unknown and null always may.
func (t typ) acceptable(kinds ...kind) bool {
	if !t.known() {
		return true
	}
	for _, k := range kinds {
		if t.k == k {
			return true
		}
	}
	return false
}

// commonElem returns the shared element type of a list literal's elements, or
// unknown when they differ.
func commonElem(ts []typ) typ {
	if len(ts) == 0 {
		return tAny
	}
	first := ts[0]
	for _, t := range ts[1:] {
		if t.k != first.k {
			return tAny
		}
	}
	return first
}

// scope maps variable names to their types. A scope that is open accepts any
// name, which is used after constructs whose output columns are not known
// statically (CALL … YIELD *).
type scope struct {
	vars   map[string]typ
	parent *scope
	open   bool
}

func newScope(parent *scope) *scope {
	return &scope{vars: map[string]typ{}, parent: parent}
}

func (s *scope) lookup(name string) (typ, bool) {
	for c := s; c != nil; c = c.parent {
		if t, ok := c.vars[name]; ok {
			return t, true
		}
	}
	return tAny, false
}

func (s *scope) isOpen() bool {
	for c := s; c != nil; c = c.parent {
		if c.open {
			return true
		}
	}
	return false
}

func (s *scope) declare(name string, t typ) { s.vars[name] = t }

// names returns every visible variable name.
func (s *scope) names() []string {
	seen := map[string]bool{}
	var out []string
	for c := s; c != nil; c = c.parent {
		for n := range c.vars {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// sameExpr reports whether two expressions are structurally identical.
func sameExpr(a, b syntax.Expr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return syntax.Dump(a) == syntax.Dump(b)
}
