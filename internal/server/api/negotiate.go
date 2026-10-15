package api

import (
	"fmt"
	"strconv"
	"strings"
)

// format is the value representation negotiated for a request.
type format int

const (
	formatPlain format = iota // Plain JSON (application/json)
	formatTyped               // Typed JSON (application/vnd.neo4j.query.v1.x)
	formatTOON                // flat TOON table (text/toon); not part of the Neo4j contract
)

// responseType is the negotiated response representation.
type responseType struct {
	format   format
	typedVer int // 0, 1 or 2; meaningful only for formatTyped
	jsonl    bool
}

// contentType returns the media type of the response body.
func (rt responseType) contentType() string {
	base := "application/json"
	switch {
	case rt.format == formatTOON:
		return mediaTOON + "; charset=utf-8"
	case rt.format == formatTyped:
		if rt.typedVer == 0 {
			base = "application/vnd.neo4j.query"
		} else {
			base = "application/vnd.neo4j.query.v1." + strconv.Itoa(rt.typedVer)
		}
	case rt.jsonl:
		base = "application/jsonl"
	}
	if rt.jsonl && rt.format == formatTyped {
		base += "+jsonl"
	}
	return base
}

// parseAccept parses the Accept header, choosing the response format.
// The header may list several media types with q-values; the supported
// type with the highest q wins, earlier entries winning ties (but a document over a stream of the same representation). The empty
// header, "*/*" and "application/*" select the default: Plain JSON.
func parseAccept(header string) (responseType, error) {
	if strings.TrimSpace(header) == "" {
		return responseType{format: formatPlain}, nil
	}
	var (
		best  responseType
		bestQ = -1.0
	)
	for _, part := range strings.Split(header, ",") {
		media, q := splitMedia(part)
		if media == "" || q <= 0 || q < bestQ {
			continue
		}
		rt, ok := acceptType(media)
		if !ok {
			continue
		}
		// On a tie the earlier entry wins, except that a single document
		// beats a stream of the same representation: a client that lists
		// both forms without preference can read the former.
		sameRep := best.format == rt.format && best.typedVer == rt.typedVer
		if q == bestQ && !(sameRep && best.jsonl && !rt.jsonl) {
			continue
		}
		best, bestQ = rt, q
	}
	if bestQ < 0 {
		return responseType{}, fmt.Errorf("unsupported Accept media type %q", header)
	}
	return best, nil
}

// splitMedia separates a media range from its parameters and returns it
// lower-cased with its q-value (1 when absent or malformed).
func splitMedia(part string) (string, float64) {
	media, params, _ := strings.Cut(strings.ToLower(strings.TrimSpace(part)), ";")
	q := 1.0
	for _, p := range strings.Split(params, ";") {
		if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.TrimSpace(k) == "q" {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				q = f
			}
		}
	}
	return strings.TrimSpace(media), q
}

// acceptType maps one media type to a response representation.
func acceptType(media string) (responseType, bool) {
	switch media {
	case "*/*", "application/*", "application/json":
		return responseType{format: formatPlain}, true
	case "application/jsonl":
		return responseType{format: formatPlain, jsonl: true}, true
	case mediaTOON:
		return responseType{format: formatTOON}, true
	case "application/vnd.neo4j.query", "application/vnd.neo4j.query.v1.0":
		return responseType{format: formatTyped, typedVer: 0}, true
	case "application/vnd.neo4j.query.v1.1":
		return responseType{format: formatTyped, typedVer: 1}, true
	case "application/vnd.neo4j.query.v1.2":
		return responseType{format: formatTyped, typedVer: 2}, true
	case "application/vnd.neo4j.query+jsonl", "application/vnd.neo4j.query.v1.0+jsonl":
		return responseType{format: formatTyped, typedVer: 0, jsonl: true}, true
	case "application/vnd.neo4j.query.v1.1+jsonl":
		return responseType{format: formatTyped, typedVer: 1, jsonl: true}, true
	case "application/vnd.neo4j.query.v1.2+jsonl":
		return responseType{format: formatTyped, typedVer: 2, jsonl: true}, true
	}
	return responseType{}, false
}

// parameterFormat reports whether request parameters must be decoded as
// Typed JSON values, based on the Content-Type header.
func parameterFormat(header string) (format, int, error) {
	media := strings.ToLower(strings.TrimSpace(header))
	if i := strings.IndexByte(media, ';'); i >= 0 {
		media = strings.TrimSpace(media[:i])
	}
	switch media {
	case "application/json":
		return formatPlain, 0, nil
	case "application/vnd.neo4j.query":
		return formatTyped, 0, nil
	case "application/vnd.neo4j.query.v1.0":
		return formatTyped, 0, nil
	case "application/vnd.neo4j.query.v1.1":
		return formatTyped, 1, nil
	case "application/vnd.neo4j.query.v1.2":
		return formatTyped, 2, nil
	}
	return formatPlain, 0, fmt.Errorf("unsupported Content-Type media type %q", header)
}
