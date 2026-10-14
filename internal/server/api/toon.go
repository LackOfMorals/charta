package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// mediaTOON is the media type that selects TOON output (https://toonformat.dev),
// a compact, line-oriented encoding that suits an LLM reading a table.
const mediaTOON = "text/toon"

// TOON is an opt-in alternative to the Neo4j Query API response body, not
// part of the Neo4j contract. It trades fidelity for tokens and for being
// hard to misread:
//
//   - one flat table, "rows": a node or relationship cell becomes columns
//     (p._id, p._labels, p.name, ...) and any other list, map or path is a
//     compact JSON string;
//   - no bookmarks or timings; the mutation counters that are non-zero are
//     always reported, without asking for them;
//   - every failure, including one raised while producing the result, is a
//     4xx/5xx with an "errors" table, never a 202.

// toonResult is the document a successful statement, transaction open or
// commit produces.
type toonResult struct {
	Tx        *txInfo
	Columns   []string
	Rows      [][]any // Plain JSON encoded values, as encodeValues returns
	Counters  *countersBlock
	Committed bool
}

// toonOf converts a response body handed to writeAccepted.
func toonOf(body any) (toonResult, bool) {
	switch b := body.(type) {
	case *resultResponse:
		res := toonResult{Tx: b.Transaction, Counters: b.Counters}
		if b.Data != nil {
			res.Columns = b.Data.Fields
			for _, v := range b.Data.Values {
				row, _ := v.([]any)
				res.Rows = append(res.Rows, row)
			}
		}
		return res, true
	case map[string]any:
		// A commit with no final statement answers with its bookmark.
		if _, ok := b["bookmarks"]; ok && len(b) == 1 {
			return toonResult{Committed: true}, true
		}
	}
	return toonResult{}, false
}

func (t toonResult) String() string {
	var sb strings.Builder
	if t.Committed {
		sb.WriteString("committed: true\n")
		return sb.String()
	}
	if t.Tx != nil {
		sb.WriteString("transaction:\n  id: " + toonString(t.Tx.ID) + "\n")
		if t.Tx.Expires != "" {
			sb.WriteString("  expires: " + toonString(t.Tx.Expires) + "\n")
		}
	}
	writeToonTable(&sb, "rows", t.Columns, t.Rows)
	if c := t.Counters; c != nil && c.ContainsUpdates {
		var lines []string
		for _, kv := range []struct {
			k string
			v int
		}{
			{"nodesCreated", c.NodesCreated}, {"nodesDeleted", c.NodesDeleted},
			{"relationshipsCreated", c.RelationshipsCreated}, {"relationshipsDeleted", c.RelationshipsDeleted},
			{"propertiesSet", c.PropertiesSet},
			{"labelsAdded", c.LabelsAdded}, {"labelsRemoved", c.LabelsRemoved},
			{"indexesAdded", c.IndexesAdded}, {"indexesRemoved", c.IndexesRemoved},
			{"constraintsAdded", c.ConstraintsAdded}, {"constraintsRemoved", c.ConstraintsRemoved},
		} {
			if kv.v != 0 {
				lines = append(lines, "  "+kv.k+": "+strconv.Itoa(kv.v)+"\n")
			}
		}
		if len(lines) > 0 {
			sb.WriteString("counters:\n" + strings.Join(lines, ""))
		}
	}
	return sb.String()
}

// toonErrors renders a client-facing error document.
func toonErrors(errs []errorBody) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "errors[%d]{code,message}:\n", len(errs))
	for _, e := range errs {
		sb.WriteString("  " + toonString(e.Code) + "," + toonString(e.Message) + "\n")
	}
	return sb.String()
}

// writeToon writes a TOON document with the given status.
func writeToon(w http.ResponseWriter, status int, doc string) {
	w.Header().Set("Content-Type", mediaTOON+"; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(doc))
}

// isTOON reports whether a response content type is TOON.
func isTOON(contentType string) bool { return strings.HasPrefix(contentType, mediaTOON) }

// writeToonTable writes "key[N]{cols}:" and one comma-separated line per
// row, flattening entity cells into columns. With no columns it writes
// the empty table "key[0]:".
func writeToonTable(sb *strings.Builder, key string, fields []string, rows [][]any) {
	cols, cells := flattenRows(fields, rows)
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = toonKey(c)
	}
	fmt.Fprintf(sb, "%s[%d]", key, len(cells))
	if len(names) > 0 {
		sb.WriteString("{" + strings.Join(names, ",") + "}")
	}
	sb.WriteString(":\n")
	for _, row := range cells {
		sb.WriteString("  " + strings.Join(row, ",") + "\n")
	}
}

// flatColumn identifies one output column: a returned field, or one part
// of an entity in that field. meta separates generated parts (_id,
// _labels, ...) from a property that happens to share the name.
type flatColumn struct {
	field int
	meta  bool
	sub   string
}

// flattenRows turns result rows into column names and encoded cells. A
// field holding nodes or relationships becomes one column per generated
// part and property, in first-seen order across the rows, with null where a
// row lacks it. The cell strings are ready to join with commas.
func flattenRows(fields []string, rows [][]any) ([]string, [][]string) {
	var (
		cols  []flatColumn
		index = map[flatColumn]int{}
	)
	// Pass 1: discover columns, field by field.
	for fi := range fields {
		add := func(c flatColumn) {
			if _, ok := index[c]; !ok {
				index[c] = len(cols)
				cols = append(cols, c)
			}
		}
		for _, row := range rows {
			if fi >= len(row) {
				continue
			}
			switch x := row[fi].(type) {
			case nodeJSON:
				add(flatColumn{fi, true, "_id"})
				add(flatColumn{fi, true, "_labels"})
				for _, k := range sortedKeys(x.Properties) {
					add(flatColumn{fi, false, k})
				}
			case relJSON:
				for _, m := range []string{"_id", "_type", "_start", "_end"} {
					add(flatColumn{fi, true, m})
				}
				for _, k := range sortedKeys(x.Properties) {
					add(flatColumn{fi, false, k})
				}
			default:
				add(flatColumn{fi, false, ""})
			}
		}
		if len(rows) == 0 {
			add(flatColumn{fi, false, ""})
		}
	}
	// A field that held an entity in some row and a plain value in
	// another keeps the plain value in a column named after the field.
	names := make([]string, len(cols))
	for i, c := range cols {
		switch {
		case c.sub == "":
			names[i] = fields[c.field]
		default:
			names[i] = fields[c.field] + "." + c.sub
		}
	}

	cells := make([][]string, len(rows))
	for ri, row := range rows {
		out := make([]string, len(cols))
		for i := range out {
			out[i] = "null"
		}
		set := func(c flatColumn, v any) {
			if i, ok := index[c]; ok {
				out[i] = toonCell(v)
			}
		}
		for fi := range fields {
			if fi >= len(row) {
				continue
			}
			switch x := row[fi].(type) {
			case nodeJSON:
				set(flatColumn{fi, true, "_id"}, x.ElementId)
				set(flatColumn{fi, true, "_labels"}, strings.Join(x.Labels, ":"))
				for k, v := range x.Properties {
					set(flatColumn{fi, false, k}, v)
				}
			case relJSON:
				set(flatColumn{fi, true, "_id"}, x.ElementId)
				set(flatColumn{fi, true, "_type"}, x.Type)
				set(flatColumn{fi, true, "_start"}, x.StartNodeElementId)
				set(flatColumn{fi, true, "_end"}, x.EndNodeElementId)
				for k, v := range x.Properties {
					set(flatColumn{fi, false, k}, v)
				}
			default:
				set(flatColumn{fi, false, ""}, x)
			}
		}
		cells[ri] = out
	}
	return names, cells
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toonCell encodes one value of a row. Lists, maps, paths and any entity
// nested inside them are written as a compact JSON string, which TOON
// then quotes.
func toonCell(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "null"
		}
		if x == 0 {
			return "0" // also -0
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return toonString(x)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return toonString(fmt.Sprint(v))
	}
	return toonString(strings.TrimSuffix(buf.String(), "\n"))
}

var (
	toonNumeric     = regexp.MustCompile(`(?i)^[+-]?\d+(?:\.\d+)?(?:e[+-]?\d+)?$`)
	toonLeadingZero = regexp.MustCompile(`^0\d+$`)
	toonSafeKey     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
)

// toonString quotes s unless TOON can read it back unquoted as the same
// string in a comma-delimited row.
func toonString(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if toonSafe(s) {
		return s
	}
	return toonQuote(s)
}

// toonKey quotes a field name that is not a plain identifier.
func toonKey(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if toonSafeKey.MatchString(s) {
		return s
	}
	return toonQuote(s)
}

func toonSafe(s string) bool {
	if s == "" || s == "true" || s == "false" || s == "null" {
		return false
	}
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return false
	}
	if s[0] == '-' || s[0] == '#' {
		return false
	}
	if strings.ContainsAny(s, ":\"\\[]{},") {
		return false
	}
	for _, r := range s {
		if r < 0x20 {
			return false
		}
	}
	return !toonNumeric.MatchString(s) && !toonLeadingZero.MatchString(s)
}

func toonQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			sb.WriteString(`\\`)
		case r == '"':
			sb.WriteString(`\"`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			if r == utf8.RuneError {
				r = '�'
			}
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
