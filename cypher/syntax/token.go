// Package syntax is a hand-written lexer and parser for Cypher 25 (openCypher 9
// plus Neo4j's extensions). It produces a fully typed AST and positioned syntax
// errors, and depends only on the standard library.
//
// The package must never import store/, sql/ or the parent cypher package;
// cypher imports syntax, not the other way round.
package syntax

import "fmt"

// Pos is a position in the query text. Offset is a byte offset; Line and Col
// are 1-based, and Col counts Unicode code points (not bytes) since the last
// newline.
type Pos struct {
	Offset int
	Line   int
	Col    int
}

// String formats the position as "line:col".
func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Kind identifies a token's lexical class.
type Kind uint8

// Token kinds. Cypher keywords are deliberately not token kinds: most of them
// may also be used as identifiers (labels, property keys, variables), so the
// lexer emits IDENT and records the keyword in Token.Keyword for the parser to
// interpret in context.
const (
	EOF Kind = iota
	IDENT
	PARAM
	INT
	FLOAT
	STRING

	LPAREN      // (
	RPAREN      // )
	LBRACK      // [
	RBRACK      // ]
	LBRACE      // {
	RBRACE      // }
	COMMA       // ,
	DOT         // .
	DOTDOT      // ..
	COLON       // :
	DOUBLECOLON // ::
	SEMI        // ;
	PIPE        // |
	PIPEPIPE    // ||
	AMP         // &
	BANG        // !
	PERCENT     // %
	CARET       // ^
	STAR        // *
	SLASH       // /
	PLUS        // +
	MINUS       // -
	EQ          // =
	NEQ         // <>
	LT          // <
	GT          // >
	LTE         // <=
	GTE         // >=
	EQTILDE     // =~
	PLUSEQ      // +=
	DOLLAR      // $ not followed by a parameter name, e.g. $(expr)
)

var kindNames = [...]string{
	EOF: "end of input", IDENT: "identifier", PARAM: "parameter", INT: "integer",
	FLOAT: "float", STRING: "string",
	LPAREN: "(", RPAREN: ")", LBRACK: "[", RBRACK: "]", LBRACE: "{", RBRACE: "}",
	COMMA: ",", DOT: ".", DOTDOT: "..", COLON: ":", DOUBLECOLON: "::", SEMI: ";",
	PIPE: "|", PIPEPIPE: "||", AMP: "&", BANG: "!", PERCENT: "%", CARET: "^",
	STAR: "*", SLASH: "/", PLUS: "+", MINUS: "-", EQ: "=", NEQ: "<>", LT: "<",
	GT: ">", LTE: "<=", GTE: ">=", EQTILDE: "=~", PLUSEQ: "+=", DOLLAR: "$",
}

// String returns a human-readable name for the kind, suitable for error messages.
func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Token is a lexical token.
//
// Text is the raw source text of the token (including quotes and backticks).
// Value is the decoded form: the unescaped content of a STRING, the name of a
// backtick-quoted IDENT, or the name of a PARAM (without the leading $). For
// every other kind Value equals Text. Both are substrings of the input where
// possible, so lexing allocates only for tokens that need unescaping.
//
// Keyword is set only for unquoted identifiers that spell a Cypher keyword
// (case-insensitively); it is the canonical upper-case form.
type Token struct {
	Kind    Kind
	Pos     Pos
	Text    string
	Value   string
	Keyword Keyword
}

// End returns the byte offset just past the token.
func (t Token) End() int { return t.Pos.Offset + len(t.Text) }

// Is reports whether the token is the unquoted keyword kw.
func (t Token) Is(kw Keyword) bool { return t.Kind == IDENT && t.Keyword == kw }

// Keyword is the canonical upper-case spelling of a Cypher keyword.
type Keyword string

// Cypher keywords. The list covers openCypher 9 plus Neo4j's Cypher 25
// additions; it is verified against the Cypher Manual in task-033.
const (
	KwAll          Keyword = "ALL"
	KwAnd          Keyword = "AND"
	KwAny          Keyword = "ANY"
	KwAs           Keyword = "AS"
	KwAsc          Keyword = "ASC"
	KwAscending    Keyword = "ASCENDING"
	KwBy           Keyword = "BY"
	KwCall         Keyword = "CALL"
	KwCase         Keyword = "CASE"
	KwCollect      Keyword = "COLLECT"
	KwConstraint   Keyword = "CONSTRAINT"
	KwContains     Keyword = "CONTAINS"
	KwCount        Keyword = "COUNT"
	KwCreate       Keyword = "CREATE"
	KwCSV          Keyword = "CSV"
	KwCypher       Keyword = "CYPHER"
	KwDelete       Keyword = "DELETE"
	KwDesc         Keyword = "DESC"
	KwDescending   Keyword = "DESCENDING"
	KwDetach       Keyword = "DETACH"
	KwDistinct     Keyword = "DISTINCT"
	KwDrop         Keyword = "DROP"
	KwElse         Keyword = "ELSE"
	KwEnd          Keyword = "END"
	KwEnds         Keyword = "ENDS"
	KwError        Keyword = "ERROR"
	KwExists       Keyword = "EXISTS"
	KwExplain      Keyword = "EXPLAIN"
	KwFail         Keyword = "FAIL"
	KwFalse        Keyword = "FALSE"
	KwFieldTerm    Keyword = "FIELDTERMINATOR"
	KwFilter       Keyword = "FILTER"
	KwFinish       Keyword = "FINISH"
	KwFor          Keyword = "FOR"
	KwForeach      Keyword = "FOREACH"
	KwFrom         Keyword = "FROM"
	KwGroups       Keyword = "GROUPS"
	KwHeaders      Keyword = "HEADERS"
	KwIf           Keyword = "IF"
	KwIn           Keyword = "IN"
	KwIndex        Keyword = "INDEX"
	KwInsert       Keyword = "INSERT"
	KwIs           Keyword = "IS"
	KwJoin         Keyword = "JOIN"
	KwLet          Keyword = "LET"
	KwLimit        Keyword = "LIMIT"
	KwLoad         Keyword = "LOAD"
	KwMatch        Keyword = "MATCH"
	KwMerge        Keyword = "MERGE"
	KwNext         Keyword = "NEXT"
	KwNode         Keyword = "NODE"
	KwNone         Keyword = "NONE"
	KwNormalized   Keyword = "NORMALIZED"
	KwNot          Keyword = "NOT"
	KwNull         Keyword = "NULL"
	KwOf           Keyword = "OF"
	KwOffset       Keyword = "OFFSET"
	KwOn           Keyword = "ON"
	KwOptional     Keyword = "OPTIONAL"
	KwOr           Keyword = "OR"
	KwOrder        Keyword = "ORDER"
	KwProfile      Keyword = "PROFILE"
	KwRemove       Keyword = "REMOVE"
	KwRequire      Keyword = "REQUIRE"
	KwReturn       Keyword = "RETURN"
	KwRows         Keyword = "ROWS"
	KwScan         Keyword = "SCAN"
	KwSet          Keyword = "SET"
	KwShortest     Keyword = "SHORTEST"
	KwShow         Keyword = "SHOW"
	KwSingle       Keyword = "SINGLE"
	KwSkip         Keyword = "SKIP"
	KwStarts       Keyword = "STARTS"
	KwThen         Keyword = "THEN"
	KwTransactions Keyword = "TRANSACTIONS"
	KwTrue         Keyword = "TRUE"
	KwTyped        Keyword = "TYPED"
	KwUnion        Keyword = "UNION"
	KwUnique       Keyword = "UNIQUE"
	KwUnwind       Keyword = "UNWIND"
	KwUsing        Keyword = "USING"
	KwWhen         Keyword = "WHEN"
	KwWhere        Keyword = "WHERE"
	KwWith         Keyword = "WITH"
	KwXor          Keyword = "XOR"
	KwYield        Keyword = "YIELD"
)

var allKeywords = [...]Keyword{
	KwAll, KwAnd, KwAny, KwAs, KwAsc, KwAscending, KwBy, KwCall, KwCase, KwCollect,
	KwConstraint, KwContains, KwCount, KwCreate, KwCSV, KwCypher, KwDelete, KwDesc,
	KwDescending, KwDetach, KwDistinct, KwDrop, KwElse, KwEnd, KwEnds, KwError,
	KwExists, KwExplain, KwFail, KwFalse, KwFieldTerm, KwFilter, KwFinish, KwFor,
	KwForeach, KwFrom, KwGroups, KwHeaders, KwIf, KwIn, KwIndex, KwInsert, KwIs,
	KwJoin, KwLet, KwLimit, KwLoad, KwMatch, KwMerge, KwNext, KwNode, KwNone,
	KwNormalized, KwNot, KwNull, KwOf, KwOffset, KwOn, KwOptional, KwOr, KwOrder,
	KwProfile, KwRemove, KwRequire, KwReturn, KwRows, KwScan, KwSet, KwShortest,
	KwShow, KwSingle, KwSkip, KwStarts, KwThen, KwTransactions, KwTrue, KwTyped,
	KwUnion, KwUnique, KwUnwind, KwUsing, KwWhen, KwWhere, KwWith, KwXor, KwYield,
}

var keywordSet = func() map[string]Keyword {
	m := make(map[string]Keyword, len(allKeywords))
	for _, k := range allKeywords {
		m[string(k)] = k
	}
	return m
}()

const maxKeywordLen = len(KwFieldTerm)

// LookupKeyword returns the keyword spelled by word, ignoring ASCII case.
// It does not allocate.
func LookupKeyword(word string) (Keyword, bool) {
	if len(word) < 2 || len(word) > maxKeywordLen {
		return "", false
	}
	var buf [maxKeywordLen]byte
	for i := 0; i < len(word); i++ {
		c := word[i]
		if c >= 0x80 {
			return "", false
		}
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		buf[i] = c
	}
	kw, ok := keywordSet[string(buf[:len(word)])]
	return kw, ok
}
