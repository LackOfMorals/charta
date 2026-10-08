package syntax

// This file defines the typed AST produced by Parse. Design rules:
//
//   - Every node embeds Loc and so reports the Pos where it starts.
//   - Interfaces are sealed (unexported marker methods); consumers dispatch with
//     a type switch.
//   - Nothing is held as unparsed text, with one deliberate exception:
//     ProjectionItem.Source keeps the verbatim source of a projected
//     expression, because un-aliased result columns are named after it. It is
//     never re-parsed.
//
// # Grammar coverage checklist
//
// Each production in the Cypher 25 grammar (openCypher 9 plus Neo4j
// extensions) and the node that represents it. Parsing of each row lands in
// the iteration named in .plans/tasks-handwritten-cypher-parser.yml.
//
//	Statements
//	  statement prefix EXPLAIN/PROFILE, CYPHER 25 [opts]  Statement
//	  query (clauses)                                     SingleQuery
//	  UNION / UNION ALL                                   UnionQuery
//	  WHEN … THEN … ELSE …                                Conditional
//	  CREATE|DROP INDEX / CONSTRAINT, SHOW …              CreateIndex, CreateConstraint, DropSchema, Show
//	  USE, access control, other server-only commands     ServerCommand
//
//	Clauses
//	  MATCH / OPTIONAL MATCH [WHERE]                      Match
//	  UNWIND                                              Unwind
//	  CREATE                                              Create
//	  MERGE [ON CREATE|MATCH SET]                         Merge, MergeAction
//	  SET (n.p =, n =, n +=, n:Label)                     Set, SetItem
//	  REMOVE (n.p, n:Label)                               Remove, RemoveItem
//	  [DETACH] DELETE                                     Delete
//	  FOREACH                                             Foreach
//	  CALL proc [YIELD …], OPTIONAL CALL                  Call, Yield
//	  CALL { subquery } [IN TRANSACTIONS]                 CallSubquery, InTransactions
//	  LOAD CSV                                            LoadCSV
//	  WITH / RETURN [DISTINCT|*] ORDER BY SKIP|OFFSET LIMIT  With, Return, Projection, ProjectionItem, SortItem
//	  FILTER, LET, FINISH                                 Filter, Let, Finish
//
//	Patterns
//	  pattern part, named path, shortestPath()            PatternPart (Var, Func)
//	  path selectors ANY/ALL/SHORTEST k [GROUPS]          PathSelector
//	  node pattern (labels, props, WHERE)                 NodePattern
//	  relationship (types, direction, *range, props)      RelPattern, Range
//	  parenthesised / quantified path pattern             GroupPattern, PathQuant
//	  label & type expressions (: & | ! % $())            LabelName, LabelWildcard, LabelNot, LabelAnd, LabelOr
//
//	Expressions
//	  literals                                            IntLit, FloatLit, StringLit, BoolLit, NullLit, ListLit, MapLit
//	  $param, variable                                    Param, Ident
//	  a.b, a[i], a[i..j]                                  Property, Subscript, Slice
//	  unary + - NOT                                       Unary
//	  OR XOR AND, + - * / % ^, ||, IN, STARTS/ENDS/CONTAINS, =~   Binary
//	  = <> < > <= >= (chained)                            Comparison
//	  IS [NOT] NULL                                       IsNull
//	  IS [NOT] :: TYPE, IS [NOT] NORMALIZED               TypePredicate, Normalized
//	  n:Label predicate                                   HasLabels
//	  function call, count(*), DISTINCT, namespaces       FuncCall
//	  CASE                                                Case
//	  [x IN l WHERE p | e]                                ListComp
//	  [(a)-->(b) WHERE p | e]                             PatternComp
//	  all/any/none/single(x IN l WHERE p)                 Quantifier
//	  reduce(acc = i, x IN l | e)                         Reduce
//	  EXISTS/COUNT/COLLECT { … }                          SubqueryExpr
//	  pattern predicate, shortestPath(…)                  PatternExpr
//	  n{.a, k: v, .*, v}                                  MapProjection

// Loc carries the start position of a node.
type Loc struct{ At Pos }

// Pos returns the position where the node starts.
func (l Loc) Pos() Pos { return l.At }

// Node is implemented by every AST node.
type Node interface{ Pos() Pos }

// ─────────────────────────────────────────────────────────────────────────────
// Statements
// ─────────────────────────────────────────────────────────────────────────────

// Statement is the root node of a parsed query.
type Statement struct {
	Loc
	// Src is the query text that was parsed.
	Src string
	// Mode is the EXPLAIN/PROFILE prefix, if any.
	Mode ExplainMode
	// Version is the language version of a `CYPHER 25` prefix, or "".
	Version string
	// Options holds `CYPHER 25 key=value …` options, in source order.
	Options []QueryOption
	// Body is the statement proper.
	Body Body
}

// ExplainMode is a statement prefix.
type ExplainMode uint8

const (
	ModeNone ExplainMode = iota
	ModeExplain
	ModeProfile
)

// QueryOption is one `key=value` option of the CYPHER prefix.
type QueryOption struct {
	Loc
	Key   string
	Value string
}

// Body is the executable part of a Statement.
type Body interface {
	Node
	bodyNode()
}

// SingleQuery is an ordered sequence of clauses.
type SingleQuery struct {
	Loc
	Clauses []Clause
}

// UnionQuery combines queries with UNION [ALL]. Mixing UNION and UNION ALL in
// one statement is a syntax error, so a single flag suffices.
type UnionQuery struct {
	Loc
	Queries []*SingleQuery
	All     bool
}

// CreateIndex is CREATE [kind] INDEX [name] [IF NOT EXISTS] FOR … ON ….
type CreateIndex struct {
	Loc
	Kind        string // RANGE, TEXT, POINT, VECTOR, FULLTEXT, LOOKUP, or "" for the default
	Name        string
	IfNotExists bool
	// Target is the NodePattern or RelPattern the index is FOR.
	Target PatternElem
	// Properties are the indexed property expressions (n.prop, …). For a
	// LOOKUP index they hold the single labels(n)/type(r) call.
	Properties []Expr
	// Each is true for `ON EACH …` (LOOKUP and FULLTEXT indexes).
	Each    bool
	Options Expr // OPTIONS map, or nil
}

// CreateConstraint is CREATE CONSTRAINT [name] [IF NOT EXISTS] FOR … REQUIRE ….
type CreateConstraint struct {
	Loc
	Name        string
	IfNotExists bool
	Target      PatternElem
	Properties  []Expr
	Kind        ConstraintKind
	// ValueType is the type for ConstraintType, e.g. "STRING".
	ValueType string
	Options   Expr
}

// ConstraintKind is the kind of a constraint.
type ConstraintKind uint8

const (
	ConstraintUnique ConstraintKind = iota
	ConstraintNotNull
	ConstraintKey
	ConstraintType
)

// DropSchema is DROP INDEX|CONSTRAINT name [IF EXISTS].
type DropSchema struct {
	Loc
	Constraint bool // false means INDEX
	Name       string
	IfExists   bool
}

// Show is SHOW <what> [YIELD …] [WHERE …]; What is the upper-cased command
// words, e.g. "INDEXES" or "CONSTRAINTS".
type Show struct {
	Loc
	What  string
	Yield *Yield
	Where Expr
}

// ServerCommand is a Neo4j server-only command (USE, user/role/database
// management, GRANT/DENY…). It is parsed so the planner can say it is not
// supported rather than reporting a syntax error.
type ServerCommand struct {
	Loc
	Command string // leading keyword(s), upper-cased
}

// Conditional is WHEN cond THEN query [WHEN …] [ELSE query].
type Conditional struct {
	Loc
	Branches []WhenBranch
	Else     Body
}

// WhenBranch is one WHEN … THEN … arm.
type WhenBranch struct {
	Loc
	Cond Expr
	Body Body
}

func (*SingleQuery) bodyNode()      {}
func (*UnionQuery) bodyNode()       {}
func (*CreateIndex) bodyNode()      {}
func (*CreateConstraint) bodyNode() {}
func (*DropSchema) bodyNode()       {}
func (*Show) bodyNode()             {}
func (*ServerCommand) bodyNode()    {}
func (*Conditional) bodyNode()      {}

// ─────────────────────────────────────────────────────────────────────────────
// Clauses
// ─────────────────────────────────────────────────────────────────────────────

// Clause is implemented by every clause node.
type Clause interface {
	Node
	clauseNode()
}

// Match is MATCH or OPTIONAL MATCH.
type Match struct {
	Loc
	Optional bool
	Mode     MatchMode
	Patterns []*PatternPart
	Hints    []Hint
	Where    Expr
}

// MatchMode is the optional DIFFERENT RELATIONSHIPS / REPEATABLE ELEMENTS mode.
type MatchMode uint8

const (
	MatchModeDefault MatchMode = iota
	MatchDifferentRelationships
	MatchRepeatableElements
)

// Hint is a planner hint (USING INDEX/JOIN/SCAN). Parsed and ignored.
type Hint struct {
	Loc
	Kind string // "INDEX", "JOIN", "SCAN", …
	Text string // hint arguments are not interpreted
}

// Unwind is UNWIND expr AS var.
type Unwind struct {
	Loc
	Expr Expr
	Var  string
}

// Create is CREATE pattern.
type Create struct {
	Loc
	Patterns []*PatternPart
}

// Merge is MERGE pattern [ON CREATE SET …] [ON MATCH SET …].
type Merge struct {
	Loc
	Pattern *PatternPart
	Actions []MergeAction
}

// MergeAction is an ON CREATE or ON MATCH SET block.
type MergeAction struct {
	Loc
	OnCreate bool // false means ON MATCH
	Items    []SetItem
}

// Set is SET item, ….
type Set struct {
	Loc
	Items []SetItem
}

// SetKind distinguishes the forms of a SET item.
type SetKind uint8

const (
	// SetProperty is `target = value` where target is a property or dynamic subscript.
	SetProperty SetKind = iota
	// SetReplace is `var = map`.
	SetReplace
	// SetMerge is `var += map`.
	SetMerge
	// SetLabels is `var:Labels`.
	SetLabels
)

// SetItem is one assignment of a SET clause (or MERGE action).
type SetItem struct {
	Loc
	Kind SetKind
	// Target is the Property/Subscript for SetProperty, or the Ident for the others.
	Target Expr
	// Labels is set for SetLabels.
	Labels LabelExpr
	// Value is the right-hand side for all kinds except SetLabels.
	Value Expr
}

// Remove is REMOVE item, ….
type Remove struct {
	Loc
	Items []RemoveItem
}

// RemoveItem is `n.prop`, `n[$k]` or `n:Labels`.
type RemoveItem struct {
	Loc
	// Target is the Property/Subscript, or the Ident when Labels is set.
	Target Expr
	Labels LabelExpr
}

// Delete is [DETACH] DELETE expr, ….
type Delete struct {
	Loc
	Detach bool
	Exprs  []Expr
}

// Foreach is FOREACH (var IN list | updating clauses).
type Foreach struct {
	Loc
	Var  string
	In   Expr
	Body []Clause
}

// Call is a procedure call, standalone or in-query.
type Call struct {
	Loc
	Optional bool
	// Name is the dotted procedure name, e.g. ["db", "labels"].
	Name []string
	Args []Expr
	// ArgsOmitted is true for a standalone `CALL proc` without parentheses
	// (implicit arguments from parameters).
	ArgsOmitted bool
	Yield       *Yield
}

// Yield is YIELD items [WHERE expr] or YIELD *.
type Yield struct {
	Loc
	Star  bool
	Items []YieldItem
	Where Expr
}

// YieldItem is `name [AS alias]`.
type YieldItem struct {
	Loc
	Name  string
	Alias string
}

// CallSubquery is CALL [(imports)] { query } [IN TRANSACTIONS …].
type CallSubquery struct {
	Loc
	Optional bool
	// Scoped is true when a variable-scope clause `(a, b)`, `(*)` or `()` is
	// present; without one, imports (if any) come from a leading WITH in Body.
	Scoped bool
	// ImportAll is true for `CALL (*) { … }`.
	ImportAll bool
	Imports   []string
	Body      Body
	InTx      *InTransactions
}

// InTransactions is the IN TRANSACTIONS suffix of CALL {}. Parsed; executed as
// a single transaction or rejected by the planner.
type InTransactions struct {
	Loc
	// Concurrent is true for IN [n] CONCURRENT TRANSACTIONS; Concurrency is
	// the optional n.
	Concurrent  bool
	Concurrency Expr
	BatchSize   Expr   // OF n ROWS, or nil
	Disjoint    string // NONE, AUTO, or "" (an expression list is not kept)
	ReportAs    string // REPORT STATUS AS name, or ""
	OnError     string // CONTINUE, BREAK, FAIL, RETRY, or ""
	RetryFor    Expr   // RETRY FOR n SECONDS, or nil
	RetryThen   string // CONTINUE, BREAK, FAIL, or ""
}

// LoadCSV is LOAD CSV [WITH HEADERS] FROM url AS var [FIELDTERMINATOR s].
type LoadCSV struct {
	Loc
	WithHeaders     bool
	From            Expr
	Var             string
	FieldTerminator Expr
}

// With is WITH projection [WHERE expr].
type With struct {
	Loc
	Projection
	Where Expr
}

// Return is RETURN projection.
type Return struct {
	Loc
	Projection
}

// Projection is the shared body of WITH and RETURN.
type Projection struct {
	Distinct bool
	// Star is true for `*` (which may be followed by further Items).
	Star  bool
	Items []ProjectionItem
	Order []SortItem
	// Skip holds SKIP or its Cypher 25 alias OFFSET.
	Skip  Expr
	Limit Expr
}

// ProjectionItem is `expr [AS alias]`.
type ProjectionItem struct {
	Loc
	Expr  Expr
	Alias string // empty when absent
	// Source is the verbatim source text of Expr. It exists only to name
	// un-aliased result columns; it is never re-parsed.
	Source string
}

// SortItem is one ORDER BY key.
type SortItem struct {
	Loc
	Expr Expr
	Desc bool
}

// Filter is the Cypher 25 FILTER [WHERE] expr clause.
type Filter struct {
	Loc
	Cond Expr
}

// Let is the Cypher 25 LET var = expr, … clause.
type Let struct {
	Loc
	Items []LetItem
}

// LetItem is `var = expr`.
type LetItem struct {
	Loc
	Var  string
	Expr Expr
}

// Finish is the Cypher 25 FINISH clause (no result).
type Finish struct{ Loc }

func (*Match) clauseNode()        {}
func (*Unwind) clauseNode()       {}
func (*Create) clauseNode()       {}
func (*Merge) clauseNode()        {}
func (*Set) clauseNode()          {}
func (*Remove) clauseNode()       {}
func (*Delete) clauseNode()       {}
func (*Foreach) clauseNode()      {}
func (*Call) clauseNode()         {}
func (*CallSubquery) clauseNode() {}
func (*LoadCSV) clauseNode()      {}
func (*With) clauseNode()         {}
func (*Return) clauseNode()       {}
func (*Filter) clauseNode()       {}
func (*Let) clauseNode()          {}
func (*Finish) clauseNode()       {}
func (*Conditional) clauseNode()  {}

// ─────────────────────────────────────────────────────────────────────────────
// Patterns
// ─────────────────────────────────────────────────────────────────────────────

// PatternPart is one comma-separated element of a pattern: an optionally
// named, optionally selector-prefixed path.
type PatternPart struct {
	Loc
	// Var is the path variable of `p = …`, or "".
	Var string
	// Selector is the Neo4j path selector (ANY SHORTEST …), or nil.
	Selector *PathSelector
	// Func is the openCypher shortestPath()/allShortestPaths() wrapper, if any.
	Func ShortestFunc
	// Elems alternates NodePattern and RelPattern, though a GroupPattern may
	// stand in for a (node, relationship, node…) run. Always starts with a
	// node pattern or a group pattern.
	Elems []PatternElem
}

// ShortestFunc is the shortestPath()/allShortestPaths() wrapper of a pattern.
type ShortestFunc uint8

const (
	FuncNone ShortestFunc = iota
	FuncShortestPath
	FuncAllShortestPaths
)

// PathSelector is a Neo4j path selector prefix.
type PathSelector struct {
	Loc
	Kind SelectorKind
	// K is the path/group count for SelectorAny, SelectorShortest and
	// SelectorShortestGroups (an IntLit or Param); nil means the default of 1.
	K Expr
}

// SelectorKind is the kind of path selector.
type SelectorKind uint8

const (
	SelectorAny            SelectorKind = iota // ANY [k]
	SelectorAll                                // ALL
	SelectorAnyShortest                        // ANY SHORTEST
	SelectorAllShortest                        // ALL SHORTEST
	SelectorShortest                           // SHORTEST k
	SelectorShortestGroups                     // SHORTEST k GROUPS
)

// PatternElem is a NodePattern, RelPattern or GroupPattern.
type PatternElem interface {
	Node
	patternElem()
}

// NodePattern is `(var:Labels {props} WHERE cond)`.
type NodePattern struct {
	Loc
	Var    string
	Labels LabelExpr // nil when absent
	// Props is a *MapLit or *Param, or nil.
	Props Expr
	Where Expr
}

// Direction is the arrow direction of a relationship pattern.
type Direction uint8

const (
	DirNone  Direction = iota // -[]-
	DirRight                  // -[]->
	DirLeft                   // <-[]-
	DirBoth                   // <-[]->
)

// RelPattern is `-[var:Types *range {props} WHERE cond]->`, optionally
// followed by a quantifier (`->+`, `->{1,3}`).
type RelPattern struct {
	Loc
	Var   string
	Types LabelExpr // relationship type expression; nil when absent
	Dir   Direction
	// Range is the variable-length `*min..max` range, or nil for a single hop.
	Range *Range
	Props Expr
	Where Expr
	// Quant is a Neo4j quantified-relationship suffix, or nil.
	Quant *PathQuant
}

// Range is the `*min..max` of a variable-length relationship. A nil bound is
// open; `*n` sets both bounds to n; bare `*` leaves both nil.
type Range struct {
	Loc
	Min *int64
	Max *int64
}

// GroupPattern is a parenthesised path pattern, optionally quantified:
// `((a)-[:R]->(b) WHERE cond){1,5}`.
type GroupPattern struct {
	Loc
	// Var is the path variable of `(p = …)`, or "".
	Var   string
	Elems []PatternElem
	Where Expr
	// Quant is nil for an unquantified group.
	Quant *PathQuant
}

// PathQuant is a normalised quantifier: `+` is {1,}, `*` is {0,}, `{n}` is
// {n,n}. A nil Max means unbounded.
type PathQuant struct {
	Loc
	Min int64
	Max *int64
}

func (*NodePattern) patternElem()  {}
func (*RelPattern) patternElem()   {}
func (*GroupPattern) patternElem() {}

// LabelExpr is a label or relationship-type expression.
type LabelExpr interface {
	Node
	labelExpr()
}

// LabelName is a single label/type, either literal or dynamic `$(expr)`.
type LabelName struct {
	Loc
	Name    string
	Dynamic Expr // set instead of Name for $(expr)
}

// LabelWildcard is `%`.
type LabelWildcard struct{ Loc }

// LabelNot is `!X`.
type LabelNot struct {
	Loc
	X LabelExpr
}

// LabelAnd is `A&B`; the legacy chain `:A:B` also produces it.
type LabelAnd struct {
	Loc
	L, R LabelExpr
}

// LabelOr is `A|B`.
type LabelOr struct {
	Loc
	L, R LabelExpr
}

func (*LabelName) labelExpr()     {}
func (*LabelWildcard) labelExpr() {}
func (*LabelNot) labelExpr()      {}
func (*LabelAnd) labelExpr()      {}
func (*LabelOr) labelExpr()       {}

// ─────────────────────────────────────────────────────────────────────────────
// Expressions
// ─────────────────────────────────────────────────────────────────────────────

// Expr is implemented by every expression node.
type Expr interface {
	Node
	exprNode()
}

// IntLit is an integer literal. Text is the source spelling (0x…, 0o…).
type IntLit struct {
	Loc
	Value int64
	Text  string
}

// FloatLit is a floating-point literal.
type FloatLit struct {
	Loc
	Value float64
	Text  string
}

// StringLit is a string literal with escapes decoded.
type StringLit struct {
	Loc
	Value string
}

// BoolLit is TRUE or FALSE.
type BoolLit struct {
	Loc
	Value bool
}

// NullLit is NULL.
type NullLit struct{ Loc }

// ListLit is `[a, b, c]`.
type ListLit struct {
	Loc
	Elems []Expr
}

// MapLit is `{k: v, …}`.
type MapLit struct {
	Loc
	Entries []MapEntry
}

// MapEntry is one `key: value` of a map literal.
type MapEntry struct {
	Loc
	Key   string
	Value Expr
}

// Param is `$name` or `$0`.
type Param struct {
	Loc
	Name string
}

// Ident is a variable reference.
type Ident struct {
	Loc
	Name string
}

// Property is `subject.key`.
type Property struct {
	Loc
	Subject Expr
	Key     string
}

// Subscript is `subject[index]`.
type Subscript struct {
	Loc
	Subject Expr
	Index   Expr
}

// Slice is `subject[from..to]`; either bound may be nil.
type Slice struct {
	Loc
	Subject  Expr
	From, To Expr
}

// UnaryOp is a prefix operator.
type UnaryOp string

const (
	OpPlus  UnaryOp = "+"
	OpMinus UnaryOp = "-"
	OpNot   UnaryOp = "NOT"
)

// Unary is a prefix operator application.
type Unary struct {
	Loc
	Op UnaryOp
	X  Expr
}

// BinaryOp is a binary operator other than the chainable comparisons.
type BinaryOp string

const (
	OpOr         BinaryOp = "OR"
	OpXor        BinaryOp = "XOR"
	OpAnd        BinaryOp = "AND"
	OpAdd        BinaryOp = "+"
	OpSub        BinaryOp = "-"
	OpMul        BinaryOp = "*"
	OpDiv        BinaryOp = "/"
	OpMod        BinaryOp = "%"
	OpPow        BinaryOp = "^"
	OpConcat     BinaryOp = "||"
	OpIn         BinaryOp = "IN"
	OpStartsWith BinaryOp = "STARTS WITH"
	OpEndsWith   BinaryOp = "ENDS WITH"
	OpContains   BinaryOp = "CONTAINS"
	OpRegexMatch BinaryOp = "=~"
)

// Binary is a binary operator application.
type Binary struct {
	Loc
	Op   BinaryOp
	L, R Expr
}

// CompareOp is a chainable comparison operator.
type CompareOp string

const (
	CmpEq  CompareOp = "="
	CmpNeq CompareOp = "<>"
	CmpLt  CompareOp = "<"
	CmpGt  CompareOp = ">"
	CmpLte CompareOp = "<="
	CmpGte CompareOp = ">="
)

// Comparison is a (possibly chained) comparison: `a < b <= c` has
// Operands [a b c] and Ops [< <=], and means `a < b AND b <= c`.
// len(Operands) == len(Ops)+1.
type Comparison struct {
	Loc
	Operands []Expr
	Ops      []CompareOp
}

// IsNull is `x IS [NOT] NULL`.
type IsNull struct {
	Loc
	X       Expr
	Negated bool
}

// TypePredicate is `x IS [NOT] :: TYPE`.
type TypePredicate struct {
	Loc
	X       Expr
	Type    string // normalised type text, e.g. "LIST<INTEGER>"
	Negated bool
}

// Normalized is `x IS [NOT] [form] NORMALIZED`.
type Normalized struct {
	Loc
	X       Expr
	Form    string // NFC, NFD, NFKC, NFKD, or "" for the default
	Negated bool
}

// HasLabels is the predicate `x:Label&…`.
type HasLabels struct {
	Loc
	X      Expr
	Labels LabelExpr
}

// FuncCall is a function invocation.
type FuncCall struct {
	Loc
	// Namespace is the dotted prefix, e.g. ["apoc", "text"] for apoc.text.join.
	Namespace []string
	Name      string
	Distinct  bool
	// Star is true for count(*).
	Star bool
	Args []Expr
}

// Case is a simple (Subject != nil) or generic CASE expression.
type Case struct {
	Loc
	Subject Expr
	Whens   []CaseWhen
	Else    Expr
}

// CaseWhen is one WHEN … THEN … arm.
type CaseWhen struct {
	Loc
	Cond Expr
	Then Expr
}

// ListComp is `[var IN list WHERE cond | proj]`; Where and Proj may be nil.
type ListComp struct {
	Loc
	Var   string
	In    Expr
	Where Expr
	Proj  Expr
}

// PatternComp is `[p = (a)-->(b) WHERE cond | proj]`.
type PatternComp struct {
	Loc
	Pattern *PatternPart
	Where   Expr
	Proj    Expr
}

// QuantifierKind is all/any/none/single.
type QuantifierKind string

const (
	QuantAll    QuantifierKind = "all"
	QuantAny    QuantifierKind = "any"
	QuantNone   QuantifierKind = "none"
	QuantSingle QuantifierKind = "single"
)

// Quantifier is `all(var IN list WHERE cond)` and its siblings.
type Quantifier struct {
	Loc
	Kind  QuantifierKind
	Var   string
	In    Expr
	Where Expr
}

// Reduce is `reduce(acc = init, var IN list | expr)`.
type Reduce struct {
	Loc
	Acc  string
	Init Expr
	Var  string
	In   Expr
	Expr Expr
}

// SubqueryKind is EXISTS/COUNT/COLLECT.
type SubqueryKind string

const (
	SubqueryExists  SubqueryKind = "EXISTS"
	SubqueryCount   SubqueryKind = "COUNT"
	SubqueryCollect SubqueryKind = "COLLECT"
)

// SubqueryExpr is `EXISTS { … }`, `COUNT { … }` or `COLLECT { … }`. The body is
// either a full query (Query) or the pattern shorthand (Patterns [+ Where]).
type SubqueryExpr struct {
	Loc
	Kind     SubqueryKind
	Query    Body
	Patterns []*PatternPart
	Where    Expr
}

// PatternExpr is a pattern used as an expression: a pattern predicate such as
// `(a)-[:R]->()` or a shortestPath()/allShortestPaths() call.
type PatternExpr struct {
	Loc
	Part *PatternPart
}

// MapProjection is `subject{.key, key: expr, var, .*}`.
type MapProjection struct {
	Loc
	Subject Expr
	Items   []MapProjItem
}

// MapProjKind is the kind of map-projection item.
type MapProjKind uint8

const (
	ProjProperty MapProjKind = iota // .key
	ProjLiteral                     // key: expr
	ProjVariable                    // var
	ProjAll                         // .*
)

// MapProjItem is one element of a map projection.
type MapProjItem struct {
	Loc
	Kind  MapProjKind
	Key   string
	Value Expr // ProjLiteral only
}

func (*IntLit) exprNode()        {}
func (*FloatLit) exprNode()      {}
func (*StringLit) exprNode()     {}
func (*BoolLit) exprNode()       {}
func (*NullLit) exprNode()       {}
func (*ListLit) exprNode()       {}
func (*MapLit) exprNode()        {}
func (*Param) exprNode()         {}
func (*Ident) exprNode()         {}
func (*Property) exprNode()      {}
func (*Subscript) exprNode()     {}
func (*Slice) exprNode()         {}
func (*Unary) exprNode()         {}
func (*Binary) exprNode()        {}
func (*Comparison) exprNode()    {}
func (*IsNull) exprNode()        {}
func (*TypePredicate) exprNode() {}
func (*Normalized) exprNode()    {}
func (*HasLabels) exprNode()     {}
func (*FuncCall) exprNode()      {}
func (*Case) exprNode()          {}
func (*ListComp) exprNode()      {}
func (*PatternComp) exprNode()   {}
func (*Quantifier) exprNode()    {}
func (*Reduce) exprNode()        {}
func (*SubqueryExpr) exprNode()  {}
func (*PatternExpr) exprNode()   {}
func (*MapProjection) exprNode() {}
