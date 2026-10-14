package api

import (
	"encoding/base64"
	"fmt"
	"math"

	"github.com/LackOfMorals/charta"
	"github.com/LackOfMorals/charta/cypher/spatial"
	"github.com/LackOfMorals/charta/cypher/temporal"
	"github.com/LackOfMorals/charta/cypher/vector"
)

// nodeJSON is the Plain JSON representation of a graph node.
type nodeJSON struct {
	ElementId  string         `json:"elementId"`
	Labels     []string       `json:"labels"`
	Properties map[string]any `json:"properties"`
}

// relJSON is the Plain JSON representation of a relationship.
type relJSON struct {
	ElementId          string         `json:"elementId"`
	StartNodeElementId string         `json:"startNodeElementId"`
	EndNodeElementId   string         `json:"endNodeElementId"`
	Type               string         `json:"type"`
	Properties         map[string]any `json:"properties"`
}

// encodePlain converts a charta result value to its Plain JSON form
// (accept: application/json).
func encodePlain(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case bool:
		return x, nil
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		return uintAsInt64(uint64(x))
	case uint8:
		return uintAsInt64(uint64(x))
	case uint16:
		return uintAsInt64(uint64(x))
	case uint32:
		return uintAsInt64(uint64(x))
	case uint64:
		return uintAsInt64(x)
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	case string:
		return x, nil
	case []byte:
		return base64.StdEncoding.EncodeToString(x), nil

	case *charta.Node:
		props, err := encodeProps(x.Props)
		if err != nil {
			return nil, err
		}
		labels := x.Labels
		if labels == nil {
			labels = []string{}
		}
		return nodeJSON{ElementId: x.ElementId, Labels: labels, Properties: props}, nil

	case *charta.Relationship:
		props, err := encodeProps(x.Props)
		if err != nil {
			return nil, err
		}
		return relJSON{
			ElementId:          x.ElementId,
			StartNodeElementId: x.StartElementId,
			EndNodeElementId:   x.EndElementId,
			Type:               x.Type,
			Properties:         props,
		}, nil

	case charta.Path:
		return encodePathPlain(x)

	case temporal.Value:
		return temporalString(x, false), nil

	case spatial.Point:
		return wktOf(x), nil

	case vector.Vector:
		out := make([]any, 0, x.Dimension())
		if x.Type.IsInteger() {
			for _, i := range x.Ints {
				out = append(out, i)
			}
		} else {
			for _, f := range x.Floats {
				out = append(out, f)
			}
		}
		return out, nil

	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			enc, err := encodePlain(e)
			if err != nil {
				return nil, err
			}
			out[i] = enc
		}
		return out, nil

	case map[string]any:
		return encodeProps(x)

	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, nil

	case []int:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = int64(e)
		}
		return out, nil

	case []int64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, nil

	case []float64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, nil

	case []bool:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, nil

	default:
		return nil, fmt.Errorf("unsupported value type %T for Plain JSON encoding", v)
	}
}

func encodePathPlain(p charta.Path) (any, error) {
	out := make([]any, 0, len(p.Nodes)+len(p.Relationships))
	for i, n := range p.Nodes {
		enc, err := encodePlain(&n)
		if err != nil {
			return nil, err
		}
		out = append(out, enc)
		if i < len(p.Relationships) {
			enc, err := encodePlain(&p.Relationships[i])
			if err != nil {
				return nil, err
			}
			out = append(out, enc)
		}
	}
	return out, nil
}

func encodeProps(m map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, val := range m {
		enc, err := encodePlain(val)
		if err != nil {
			return nil, err
		}
		out[k] = enc
	}
	return out, nil
}

// uintAsInt64 converts an unsigned integer to int64 for JSON emission,
// rejecting values that would overflow.
func uintAsInt64(u uint64) (any, error) {
	if u > uint64(math.MaxInt64) {
		return nil, fmt.Errorf("integer %d exceeds the supported 64-bit range", u)
	}
	return int64(u), nil
}
