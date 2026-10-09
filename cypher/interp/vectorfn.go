package interp

import (
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/vector"
)

// vectorFunction evaluates vector(), vector_distance(), vector_norm(),
// vector_dimension_count() and vector.similarity.*.
func vectorFunction(ns []string, name string, args []any) (any, bool, error) {
	var fn string
	switch {
	case len(ns) == 0:
		fn = strings.ToLower(name)
	case len(ns) == 2 && strings.EqualFold(ns[0], "vector") && strings.EqualFold(ns[1], "similarity"):
		fn = "vector.similarity." + strings.ToLower(name)
	default:
		return nil, false, nil
	}
	switch fn {
	case "vector":
		if err := argc("vector", args, 3, 3); err != nil {
			return nil, true, err
		}
		if args[0] == nil || args[1] == nil || args[2] == nil {
			return nil, true, nil
		}
		dim, ok := args[1].(int64)
		if !ok {
			return nil, true, typeErr("vector() dimension must be an integer, got %s", typeName(args[1]))
		}
		tname, ok := args[2].(string)
		if !ok {
			return nil, true, typeErr("vector() coordinate type must be a type name")
		}
		t, ok := vector.ParseType(tname)
		if !ok {
			return nil, true, argErr("unknown vector coordinate type %q", tname)
		}
		var values []any
		switch x := args[0].(type) {
		case []any:
			values = x
		case string:
			var err error
			if values, err = vector.Parse(x); err != nil {
				return nil, true, argErr("%v", err)
			}
		default:
			return nil, true, typeErr("vector() expects a list of numbers or a string, got %s", typeName(args[0]))
		}
		for _, v := range values {
			if v == nil {
				return nil, true, argErr("a vector cannot contain null")
			}
		}
		res, err := vector.New(values, int(dim), t)
		if err != nil {
			return nil, true, argErr("%v", err)
		}
		return res, true, nil
	case "vector_dimension_count":
		if err := argc(fn, args, 1, 1); err != nil {
			return nil, true, err
		}
		if args[0] == nil {
			return nil, true, nil
		}
		v, ok := args[0].(vector.Vector)
		if !ok {
			return nil, true, typeErr("%s() expects a vector, got %s", fn, typeName(args[0]))
		}
		return int64(v.Dimension()), true, nil
	case "vector_norm":
		if err := argc(fn, args, 2, 2); err != nil {
			return nil, true, err
		}
		if args[0] == nil || args[1] == nil {
			return nil, true, nil
		}
		v, ok := args[0].(vector.Vector)
		kind, ok2 := args[1].(string)
		if !ok || !ok2 {
			return nil, true, typeErr("vector_norm() expects a vector and a norm name")
		}
		n, err := vector.Norm(v, kind)
		if err != nil {
			return nil, true, argErr("%v", err)
		}
		return n, true, nil
	case "vector_distance", "vector.similarity.cosine", "vector.similarity.euclidean":
		want := 2
		if fn == "vector_distance" {
			want = 3
		}
		if err := argc(fn, args, want, want); err != nil {
			return nil, true, err
		}
		for _, a := range args {
			if a == nil {
				return nil, true, nil
			}
		}
		a, ok1 := args[0].(vector.Vector)
		b, ok2 := args[1].(vector.Vector)
		if !ok1 || !ok2 {
			return nil, true, typeErr("%s() expects vectors, got %s and %s", fn, typeName(args[0]), typeName(args[1]))
		}
		var d float64
		var err error
		switch fn {
		case "vector_distance":
			metric, isStr := args[2].(string)
			if !isStr {
				return nil, true, typeErr("vector_distance() metric must be a name")
			}
			d, err = vector.Distance(a, b, metric)
		case "vector.similarity.cosine":
			d, err = vector.SimilarityCosine(a, b)
		default:
			d, err = vector.SimilarityEuclidean(a, b)
		}
		if err != nil {
			return nil, true, argErr("%v", err)
		}
		return d, true, nil
	}
	return nil, false, nil
}
