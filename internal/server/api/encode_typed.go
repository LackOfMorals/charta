package api

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/LackOfMorals/charta"
	"github.com/LackOfMorals/charta/cypher/spatial"
	"github.com/LackOfMorals/charta/cypher/temporal"
	"github.com/LackOfMorals/charta/cypher/vector"
)

// typedValue is the Typed JSON representation of a single value:
// {"$type": "<Type>", "_value": <value>}.
type typedValue struct {
	Type  string `json:"$type"`
	Value any    `json:"_value"`
}

// encodeTyped converts a charta result value to its Typed JSON form.
// ver is the Typed JSON media-type version (0, 1 or 2) and controls which
// optional types (Vector) are emitted natively.
func encodeTyped(v any, ver int) (any, error) {
	switch x := v.(type) {
	case nil:
		return typedValue{Type: "Null", Value: nil}, nil
	case bool:
		return typedValue{Type: "Boolean", Value: x}, nil
	case int:
		return typedValue{Type: "Integer", Value: strconv.FormatInt(int64(x), 10)}, nil
	case int8:
		return typedValue{Type: "Integer", Value: strconv.FormatInt(int64(x), 10)}, nil
	case int16:
		return typedValue{Type: "Integer", Value: strconv.FormatInt(int64(x), 10)}, nil
	case int32:
		return typedValue{Type: "Integer", Value: strconv.FormatInt(int64(x), 10)}, nil
	case int64:
		return typedValue{Type: "Integer", Value: strconv.FormatInt(x, 10)}, nil
	case uint:
		return uintTyped(uint64(x))
	case uint8:
		return uintTyped(uint64(x))
	case uint16:
		return uintTyped(uint64(x))
	case uint32:
		return uintTyped(uint64(x))
	case uint64:
		return uintTyped(x)
	case float32:
		return typedValue{Type: "Float", Value: formatFloat(float64(x))}, nil
	case float64:
		return typedValue{Type: "Float", Value: formatFloat(x)}, nil
	case string:
		return typedValue{Type: "String", Value: x}, nil
	case []byte:
		return typedValue{Type: "Base64", Value: base64.StdEncoding.EncodeToString(x)}, nil

	case *charta.Node:
		props, err := encodeTypedProps(x.Props, ver)
		if err != nil {
			return nil, err
		}
		labels := x.Labels
		if labels == nil {
			labels = []string{}
		}
		return typedValue{Type: "Node", Value: map[string]any{
			"_element_id": x.ElementId,
			"_labels":     labels,
			"_properties": props,
		}}, nil

	case *charta.Relationship:
		props, err := encodeTypedProps(x.Props, ver)
		if err != nil {
			return nil, err
		}
		return typedValue{Type: "Relationship", Value: map[string]any{
			"_element_id":            x.ElementId,
			"_start_node_element_id": x.StartElementId,
			"_end_node_element_id":   x.EndElementId,
			"_type":                  x.Type,
			"_properties":            props,
		}}, nil

	case charta.Path:
		return encodePathTyped(x, ver)

	case temporal.Date:
		return typedValue{Type: "Date", Value: temporalString(x, true)}, nil
	case temporal.LocalTime:
		return typedValue{Type: "LocalTime", Value: temporalString(x, true)}, nil
	case temporal.Time:
		return typedValue{Type: "Time", Value: temporalString(x, true)}, nil
	case temporal.LocalDateTime:
		return typedValue{Type: "LocalDateTime", Value: temporalString(x, true)}, nil
	case temporal.DateTime:
		s := temporalString(x, true)
		if strings.Contains(s, "[") {
			return typedValue{Type: "ZonedDateTime", Value: s}, nil
		}
		return typedValue{Type: "OffsetDateTime", Value: s}, nil
	case temporal.Duration:
		return typedValue{Type: "Duration", Value: temporalString(x, true)}, nil

	case spatial.Point:
		return typedValue{Type: "Point", Value: wktOf(x)}, nil

	case vector.Vector:
		return encodeVectorTyped(x, ver)

	case []any:
		return encodeTypedList(x, ver)

	case map[string]any:
		props, err := encodeTypedProps(x, ver)
		if err != nil {
			return nil, err
		}
		return typedValue{Type: "Map", Value: props}, nil

	case []string:
		return encodeTypedSlice(x, ver)
	case []int:
		return encodeTypedSlice(x, ver)
	case []int64:
		return encodeTypedSlice(x, ver)
	case []float64:
		return encodeTypedSlice(x, ver)
	case []bool:
		return encodeTypedSlice(x, ver)

	default:
		return typedValue{Type: "Unsupported",
			Value: fmt.Sprintf("Type %T is not supported.", v)}, nil
	}
}

func encodePathTyped(p charta.Path, ver int) (any, error) {
	out := make([]any, 0, len(p.Nodes)+len(p.Relationships))
	for i, n := range p.Nodes {
		enc, err := encodeTyped(&n, ver)
		if err != nil {
			return nil, err
		}
		out = append(out, enc)
		if i < len(p.Relationships) {
			enc, err := encodeTyped(&p.Relationships[i], ver)
			if err != nil {
				return nil, err
			}
			out = append(out, enc)
		}
	}
	return typedValue{Type: "Path", Value: out}, nil
}

func encodeVectorTyped(x vector.Vector, ver int) (any, error) {
	if ver < 1 {
		return typedValue{Type: "Unsupported", Value: "Type Vector is not supported."}, nil
	}
	coords := make([]string, 0, x.Dimension())
	if x.Type.IsInteger() {
		for _, i := range x.Ints {
			coords = append(coords, strconv.FormatInt(i, 10))
		}
	} else {
		bits := 32
		if x.Type == vector.Float64 {
			bits = 64
		}
		for _, f := range x.Floats {
			coords = append(coords, strconv.FormatFloat(f, 'g', -1, bits))
		}
	}
	return typedValue{Type: "Vector", Value: map[string]any{
		"coordinatesType": neoVectorType(x.Type),
		"coordinates":     coords,
	}}, nil
}

// neoVectorType maps a charta vector type to the Query API name.
func neoVectorType(t vector.Type) string {
	switch t {
	case vector.Integer64:
		return "INT64"
	case vector.Integer32:
		return "INT32"
	case vector.Integer16:
		return "INT16"
	case vector.Integer8:
		return "INT8"
	case vector.Float64:
		return "FLOAT64"
	case vector.Float32:
		return "FLOAT32"
	}
	return string(t)
}

func encodeTypedList(list []any, ver int) (any, error) {
	outs := make([]any, len(list))
	for i, e := range list {
		enc, err := encodeTyped(e, ver)
		if err != nil {
			return nil, err
		}
		outs[i] = enc
	}
	return typedValue{Type: "List", Value: outs}, nil
}

func encodeTypedProps(m map[string]any, ver int) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, val := range m {
		enc, err := encodeTyped(val, ver)
		if err != nil {
			return nil, err
		}
		out[k] = enc
	}
	return out, nil
}

// encodeTypedSlice encodes a homogeneous Go slice as a Typed JSON List.
func encodeTypedSlice[T any](xs []T, ver int) (any, error) {
	outs := make([]any, len(xs))
	for i, e := range xs {
		enc, err := encodeTyped(e, ver)
		if err != nil {
			return nil, err
		}
		outs[i] = enc
	}
	return typedValue{Type: "List", Value: outs}, nil
}

// uintTyped encodes an unsigned integer, rejecting values beyond the
// 64-bit signed range exactly as the Plain JSON encoder does.
func uintTyped(u uint64) (any, error) {
	if u > math.MaxInt64 {
		return nil, fmt.Errorf("integer %d exceeds the supported 64-bit range", u)
	}
	return typedValue{Type: "Integer", Value: strconv.FormatUint(u, 10)}, nil
}

// formatFloat renders a float64 with the Query API's float string rules:
// decimal notation when the magnitude is in [1e-3, 1e7), scientific
// otherwise (with Java's "1.0E21" mantissa), and the words NaN / Infinity / -Infinity for specials.
func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		if math.Signbit(v) {
			return "-0.0"
		}
		return "0.0"
	}
	av := math.Abs(v)
	if av >= 1e-3 && av < 1e7 {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	s := strconv.FormatFloat(v, 'E', -1, 64)
	i := strings.IndexByte(s, 'E')
	mant, exp := s[:i], s[i+1:]
	if !strings.Contains(mant, ".") {
		// Java writes at least two digits and picks the closest two-digit
		// decimal that still reads back as v (4.9E-324, not 5E-324).
		if two := strconv.FormatFloat(v, 'E', 1, 64); two != "" {
			if back, err := strconv.ParseFloat(two, 64); err == nil && back == v {
				s = two
				i = strings.IndexByte(s, 'E')
				mant, exp = s[:i], s[i+1:]
			}
		}
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
	}
	// strconv pads the exponent to two digits ("E+09", "E-07"); the Query
	// API wants none ("E9", "E-7"). Split the sign off before trimming.
	sign := ""
	switch exp[0] {
	case '+':
		exp = exp[1:]
	case '-':
		sign, exp = "-", exp[1:]
	}
	if exp = strings.TrimLeft(exp, "0"); exp == "" {
		exp = "0"
	}
	return mant + "E" + sign + exp
}
