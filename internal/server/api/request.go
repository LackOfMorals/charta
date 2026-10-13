package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LackOfMorals/charta/cypher/temporal"
	"github.com/LackOfMorals/charta/cypher/vector"
)

// maxBodyBytes bounds request bodies. Cypher statements plus parameters
// are normally small; the limit guards against accidental multi-gigabyte
// bodies.
const maxBodyBytes = 64 << 20 // 64 MiB

// maxClientTxTimeout caps the idle timeout a client may request with
// maxExecutionTime, so a client cannot pin a transaction (and with it the
// SQLite write lock) indefinitely.
const maxClientTxTimeout = time.Hour

// QueryRequest is the decoded request body of a query or transaction
// request.
type QueryRequest struct {
	// Statement is the Cypher statement to execute. Empty for an
	// explicit-transaction open request that only keeps the transaction
	// alive.
	Statement string
	// Parameters are the Cypher parameters, as charta-native Go values.
	Parameters map[string]any
	// IncludeCounters requests mutation counters in the response.
	IncludeCounters bool
	// TxMetadata is optional transaction metadata (transaction open only).
	TxMetadata map[string]any
	// MaxExecutionTime is the requested transaction idle timeout
	// (transaction open only), capped at maxClientTxTimeout. It is nil
	// when absent or zero, meaning the server default applies.
	MaxExecutionTime *time.Duration
}

// errInvalidInput reports a client-side request problem; handlers map it
// to a 400 with Neo.ClientError.Request.InvalidInput.
type errInvalidInput struct{ msg string }

func (e *errInvalidInput) Error() string { return e.msg }

func invalidInputf(format string, args ...any) error {
	return &errInvalidInput{msg: fmt.Sprintf(format, args...)}
}

// errUnsupportedMedia reports a Content-Type the server cannot decode;
// handlers answer it with 415.
type errUnsupportedMedia struct{ err error }

func (e *errUnsupportedMedia) Error() string { return e.err.Error() }
func (e *errUnsupportedMedia) Unwrap() error { return e.err }

// errBodyTooLarge reports a request body above maxBodyBytes; handlers
// answer it with 413.
var errBodyTooLarge = invalidInputf("request body exceeds the %d byte limit", maxBodyBytes)

// decodeQueryRequest reads and decodes the request body. The parameter
// encoding (plain vs typed JSON) is determined by the Content-Type header.
func decodeQueryRequest(body io.Reader, contentType string) (*QueryRequest, error) {
	// Read one byte past the limit so an oversized body is detected
	// rather than silently truncated.
	data, err := io.ReadAll(io.LimitReader(body, maxBodyBytes+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, errBodyTooLarge
		}
		return nil, invalidInputf("read request body: %v", err)
	}
	if len(data) > maxBodyBytes {
		return nil, errBodyTooLarge
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return &QueryRequest{}, nil
	}
	paramFmt, paramVer, err := parameterFormat(contentType)
	if err != nil {
		return nil, &errUnsupportedMedia{err: err}
	}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return nil, invalidInputf("request body must be a JSON object: %v", err)
	}
	if err := rejectTrailing(dec); err != nil {
		return nil, err
	}

	q := &QueryRequest{}
	if v, ok := raw["statement"]; ok {
		s, err := jsonString(v)
		if err != nil {
			return nil, invalidInputf(`"statement" must be a string: %v`, err)
		}
		q.Statement = s
	} else if v, ok := raw["query"]; ok {
		// Accepted for compatibility; the documented field is "statement".
		s, err := jsonString(v)
		if err != nil {
			return nil, invalidInputf(`"query" must be a string: %v`, err)
		}
		q.Statement = s
	}

	if v, ok := raw["parameters"]; ok {
		params, err := decodeParameters(v, paramFmt, paramVer)
		if err != nil {
			return nil, err
		}
		q.Parameters = params
	}
	if v, ok := raw["includeCounters"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return nil, invalidInputf(`"includeCounters" must be a boolean: %v`, err)
		}
		q.IncludeCounters = b
	}
	if v, ok := raw["txMetadata"]; ok {
		meta, err := decodeParameters(v, paramFmt, paramVer)
		if err != nil {
			return nil, invalidInputf(`invalid "txMetadata": %v`, err)
		}
		q.TxMetadata = meta
	}
	if v, ok := raw["maxExecutionTime"]; ok {
		var secs float64
		if err := json.Unmarshal(v, &secs); err != nil || secs < 0 {
			return nil, invalidInputf(`"maxExecutionTime" must be a non-negative number of seconds`)
		}
		// Zero means "use the server default", not "never expire".
		if secs > 0 {
			d := maxClientTxTimeout
			if secs < maxClientTxTimeout.Seconds() {
				d = time.Duration(secs * float64(time.Second))
			}
			q.MaxExecutionTime = &d
		}
	}
	return q, nil
}

// jsonString unmarshals a raw JSON value that must be a string.
func jsonString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// decodeParameters decodes the "parameters" object in the negotiated format.
func decodeParameters(raw json.RawMessage, pf format, ver int) (map[string]any, error) {
	switch pf {
	case formatPlain:
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			return nil, invalidInputf(`"parameters" must be a JSON object: %v`, err)
		}
		return plainParams(m), nil
	case formatTyped:
		var m map[string]typedParam
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, invalidInputf(`"parameters" must be an object of typed values: %v`, err)
		}
		out := make(map[string]any, len(m))
		for k, tp := range m {
			v, err := tp.convert(ver)
			if err != nil {
				return nil, invalidInputf("parameter $%s: %v", k, err)
			}
			out[k] = v
		}
		return out, nil
	}
	return nil, invalidInputf("unknown parameter format")
}

// plainParams converts json.Number values to int64/float64, keeping
// integers exact.
func plainParams(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = plainValue(v)
	}
	return out
}

func plainValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[k] = plainValue(val)
		}
		return m
	case []any:
		list := make([]any, len(x))
		for i, val := range x {
			list[i] = plainValue(val)
		}
		return list
	default:
		return v
	}
}

// typedParam is one Typed JSON parameter value.
type typedParam struct {
	Type  string          `json:"$type"`
	Value json.RawMessage `json:"_value"`
}

// convert turns a typed parameter into a charta-native Go value.
func (tp typedParam) convert(ver int) (any, error) {
	if len(tp.Type) == 0 {
		return nil, errors.New("missing $type")
	}
	v := tp.Value
	switch tp.Type {
	case "Null":
		return nil, nil
	case "Boolean":
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return nil, fmt.Errorf("Boolean: %w", err)
		}
		return b, nil
	case "Integer":
		n, err := parseNumber(v)
		if err != nil {
			return nil, fmt.Errorf("Integer: %w", err)
		}
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Integer: %w", err)
		}
		return i, nil
	case "Float":
		n, err := parseNumber(v)
		if err != nil {
			return nil, fmt.Errorf("Float: %w", err)
		}
		switch n {
		case "NaN":
			return math.NaN(), nil
		case "Infinity", "+Infinity":
			return math.Inf(1), nil
		case "-Infinity":
			return math.Inf(-1), nil
		}
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return nil, fmt.Errorf("Float: %w", err)
		}
		return f, nil
	case "String":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("String: %w", err)
		}
		return s, nil
	case "Base64":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("Base64: %w", err)
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("Base64: %w", err)
		}
		return b, nil
	case "List":
		var items []typedParam
		if err := json.Unmarshal(v, &items); err != nil {
			return nil, fmt.Errorf("List: %w", err)
		}
		out := make([]any, len(items))
		for i, tp := range items {
			val, err := tp.convert(ver)
			if err != nil {
				return nil, fmt.Errorf("List[%d]: %w", i, err)
			}
			out[i] = val
		}
		return out, nil
	case "Map":
		var m map[string]typedParam
		if err := json.Unmarshal(v, &m); err != nil {
			return nil, fmt.Errorf("Map: %w", err)
		}
		out := make(map[string]any, len(m))
		for k, tp := range m {
			val, err := tp.convert(ver)
			if err != nil {
				return nil, fmt.Errorf("Map[%q]: %w", k, err)
			}
			out[k] = val
		}
		return out, nil

	case "Date", "Time", "LocalTime", "LocalDateTime", "ZonedDateTime", "OffsetDateTime", "Duration":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", tp.Type, err)
		}
		kind, ok := temporal.KindByName(temporalKindName(tp.Type))
		if !ok {
			return nil, fmt.Errorf("%s: unknown temporal type", tp.Type)
		}
		return temporal.Parse(kind, s)

	case "Point":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("Point: %w", err)
		}
		return parseWKT(s)

	case "Vector":
		if ver < 1 {
			return nil, errors.New("Vector parameters require Typed JSON v1.1 or newer")
		}
		return decodeVector(v)

	case "Node", "Relationship", "Path":
		return nil, fmt.Errorf("%s values cannot be passed as parameters", tp.Type)
	default:
		return nil, fmt.Errorf("unknown $type %q", tp.Type)
	}
}

// temporalKindName maps a Query API temporal type name to the charta
// temporal kind name accepted by temporal.KindByName.
func temporalKindName(apiType string) string {
	switch apiType {
	case "Date":
		return "date"
	case "Time":
		return "time"
	case "LocalTime":
		return "localTime"
	case "LocalDateTime":
		return "localDateTime"
	case "ZonedDateTime", "OffsetDateTime":
		return "datetime"
	case "Duration":
		return "duration"
	}
	return strings.ToLower(apiType)
}

// decodeVector decodes a Typed JSON Vector value.
func decodeVector(raw json.RawMessage) (any, error) {
	var vv struct {
		CoordinatesType string   `json:"coordinatesType"`
		Coordinates     []string `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &vv); err != nil {
		return nil, fmt.Errorf("Vector: %w", err)
	}
	chartaType, ok := chartaVectorType(vv.CoordinatesType)
	if !ok {
		return nil, fmt.Errorf("Vector: unsupported coordinatesType %q", vv.CoordinatesType)
	}
	vec := vector.Vector{Type: chartaType}
	if chartaType.IsInteger() {
		vec.Ints = make([]int64, len(vv.Coordinates))
		for i, c := range vv.Coordinates {
			n, err := strconv.ParseInt(c, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Vector: coordinate %d: %w", i, err)
			}
			vec.Ints[i] = n
		}
	} else {
		bits := 32
		if chartaType == vector.Float64 {
			bits = 64
		}
		vec.Floats = make([]float64, len(vv.Coordinates))
		for i, c := range vv.Coordinates {
			f, err := strconv.ParseFloat(c, bits)
			if err != nil {
				return nil, fmt.Errorf("Vector: coordinate %d: %w", i, err)
			}
			vec.Floats[i] = f
		}
	}
	return vec, nil
}

// chartaVectorType maps a Query API vector type name to a charta type.
func chartaVectorType(name string) (vector.Type, bool) {
	switch strings.ToUpper(name) {
	case "INT64":
		return vector.Integer64, true
	case "INT32":
		return vector.Integer32, true
	case "INT16":
		return vector.Integer16, true
	case "INT8":
		return vector.Integer8, true
	case "FLOAT64":
		return vector.Float64, true
	case "FLOAT32":
		return vector.Float32, true
	}
	return "", false
}

// parseNumber unmarshals a JSON value that is a string or a number and
// returns its text form.
func parseNumber(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}
	return "", errors.New("expected a string or number")
}

// rejectTrailing fails when content follows the top-level JSON value.
func rejectTrailing(dec *json.Decoder) error {
	if _, err := dec.Token(); err != io.EOF {
		return invalidInputf("request body must contain a single JSON object")
	}
	return nil
}
