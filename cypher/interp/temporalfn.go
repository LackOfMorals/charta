package interp

import (
	"strings"
	"time"

	"github.com/LackOfMorals/graphlite/v2/cypher/spatial"
	"github.com/LackOfMorals/graphlite/v2/cypher/temporal"
)

// spatialFunction evaluates point() and the point.* functions.
func spatialFunction(ns []string, name string, args []any) (any, bool, error) {
	if len(ns) == 0 {
		switch name {
		case "point":
			if err := argc("point", args, 1, 1); err != nil {
				return nil, true, err
			}
			if args[0] == nil {
				return nil, true, nil
			}
			m, ok := args[0].(map[string]any)
			if !ok {
				return nil, true, typeErr("point() expects a map, got %s", typeName(args[0]))
			}
			for _, v := range m {
				if v == nil {
					return nil, true, nil // a null coordinate makes the point null
				}
			}
			p, err := spatial.FromMap(m)
			if err != nil {
				return nil, true, argErr("%v", err)
			}
			return p, true, nil
		}
		return nil, false, nil
	}
	if len(ns) != 1 || ns[0] != "point" {
		return nil, false, nil
	}
	switch strings.ToLower(name) {
	case "distance":
		return spatialDistance("point.distance", args)
	case "withinbbox":
		if err := argc("point.withinBBox", args, 3, 3); err != nil {
			return nil, true, err
		}
		var ps [3]spatial.Point
		for i, a := range args {
			if a == nil {
				return nil, true, nil
			}
			p, ok := a.(spatial.Point)
			if !ok {
				return nil, true, typeErr("point.withinBBox() expects points, got %s", typeName(a))
			}
			ps[i] = p
		}
		within, ok := spatial.WithinBBox(ps[0], ps[1], ps[2])
		if !ok {
			return nil, true, nil
		}
		return within, true, nil
	}
	return nil, false, nil
}

func spatialDistance(fn string, args []any) (any, bool, error) {
	if err := argc(fn, args, 2, 2); err != nil {
		return nil, true, err
	}
	if args[0] == nil || args[1] == nil {
		return nil, true, nil
	}
	a, ok1 := args[0].(spatial.Point)
	b, ok2 := args[1].(spatial.Point)
	if !ok1 || !ok2 {
		return nil, true, typeErr("%s() expects points, got %s and %s", fn, typeName(args[0]), typeName(args[1]))
	}
	d, ok := spatial.Distance(a, b)
	if !ok {
		return nil, true, nil
	}
	return d, true, nil
}

// isTemporal reports whether v is a temporal value.
func isTemporal(v any) bool {
	_, ok := v.(temporal.Value)
	return ok
}

func temporalTypeName(v temporal.Value) string {
	switch v.Kind() {
	case temporal.KindDate:
		return "Date"
	case temporal.KindLocalTime:
		return "LocalTime"
	case temporal.KindTime:
		return "Time"
	case temporal.KindLocalDateTime:
		return "LocalDateTime"
	case temporal.KindDateTime:
		return "DateTime"
	}
	return "Duration"
}

func temporalErr(err error) error {
	return errorf("ArgumentError", "InvalidArgumentValue", "%v", err)
}

// temporalFunction evaluates a temporal constructor (date, datetime, …).
// ok is false when name is not one.
func (ex *exec) temporalFunction(name string, args []any) (v any, ok bool, err error) {
	kind, isKind := temporal.KindByName(name)
	if !isKind {
		return nil, false, nil
	}
	if len(args) > 1 {
		return nil, true, argErr("%s() takes at most one argument", name)
	}
	if len(args) == 0 {
		if kind == temporal.KindDuration {
			return nil, true, argErr("duration() needs an argument")
		}
		now, err := temporal.Now(kind, nil, ex.clock)
		if err != nil {
			return nil, true, temporalErr(err)
		}
		return now, true, nil
	}
	if args[0] == nil {
		return nil, true, nil
	}
	res, err := temporal.Construct(kind, args[0])
	if err != nil {
		return nil, true, temporalErr(err)
	}
	return res, true, nil
}

// namespacedTemporal evaluates date.truncate, datetime.fromepoch,
// duration.between and the like.
func (ex *exec) namespacedTemporal(ns, name string, args []any) (v any, ok bool, err error) {
	kind, isKind := temporal.KindByName(ns)
	if !isKind {
		return nil, false, nil
	}
	name = strings.ToLower(name)
	switch name {
	case "transaction", "statement", "realtime":
		if kind == temporal.KindDuration {
			return nil, false, nil
		}
		var m map[string]any
		if len(args) == 1 {
			if args[0] == nil {
				return nil, true, nil
			}
			var isMap bool
			if m, isMap = args[0].(map[string]any); !isMap {
				return nil, true, argErr("%s.%s() expects a map", ns, name)
			}
		}
		at := ex.clock
		if name == "realtime" {
			at = time.Now()
		}
		now, err := temporal.Now(kind, m, at)
		if err != nil {
			return nil, true, temporalErr(err)
		}
		return now, true, nil
	case "truncate":
		if kind == temporal.KindDuration {
			return nil, false, nil
		}
		if len(args) < 2 || len(args) > 3 {
			return nil, true, argErr("%s.truncate() takes 2 or 3 arguments", ns)
		}
		for _, a := range args {
			if a == nil {
				return nil, true, nil
			}
		}
		unit, isStr := args[0].(string)
		other, isTemp := args[1].(temporal.Value)
		if !isStr || !isTemp {
			return nil, true, typeErr("%s.truncate() expects a unit string and a temporal value", ns)
		}
		var m map[string]any
		if len(args) == 3 {
			var isMap bool
			if m, isMap = args[2].(map[string]any); !isMap {
				return nil, true, typeErr("%s.truncate() expects a map as its third argument", ns)
			}
		}
		res, err := temporal.Truncate(kind, unit, other, m)
		if err != nil {
			return nil, true, temporalErr(err)
		}
		return res, true, nil
	case "fromepoch", "fromepochmillis":
		if kind != temporal.KindDateTime {
			return nil, false, nil
		}
		for _, a := range args {
			if a == nil {
				return nil, true, nil
			}
		}
		if name == "fromepoch" {
			if len(args) != 2 {
				return nil, true, argErr("datetime.fromepoch() takes 2 arguments")
			}
			s, ok1 := args[0].(int64)
			n, ok2 := args[1].(int64)
			if !ok1 || !ok2 {
				return nil, true, typeErr("datetime.fromepoch() expects integers")
			}
			return temporal.FromEpoch(s, n), true, nil
		}
		if len(args) != 1 {
			return nil, true, argErr("datetime.fromepochmillis() takes 1 argument")
		}
		ms, isInt := args[0].(int64)
		if !isInt {
			return nil, true, typeErr("datetime.fromepochmillis() expects an integer")
		}
		return temporal.FromEpochMillis(ms), true, nil
	case "between", "inmonths", "indays", "inseconds":
		if kind != temporal.KindDuration {
			return nil, false, nil
		}
		if len(args) != 2 {
			return nil, true, argErr("duration.%s() takes 2 arguments", name)
		}
		if args[0] == nil || args[1] == nil {
			return nil, true, nil
		}
		a, ok1 := args[0].(temporal.Value)
		b, ok2 := args[1].(temporal.Value)
		if !ok1 || !ok2 {
			return nil, true, typeErr("duration.%s() expects temporal values", name)
		}
		op := map[string]string{"between": "between", "inmonths": "inMonths", "indays": "inDays", "inseconds": "inSeconds"}[name]
		d, err := temporal.Between(op, a, b)
		if err != nil {
			return nil, true, temporalErr(err)
		}
		return d, true, nil
	}
	return nil, false, nil
}

// temporalArith evaluates + - * / when an operand is temporal. ok is false
// when neither operand is.
func temporalArith(op string, l, r any) (v any, ok bool, err error) {
	lt, lIs := l.(temporal.Value)
	rt, rIs := r.(temporal.Value)
	if !lIs && !rIs {
		return nil, false, nil
	}
	fail := func() (any, bool, error) {
		return nil, true, typeErr("cannot apply %s to %s and %s", op, typeName(l), typeName(r))
	}
	wrap := func(res temporal.Value, err error) (any, bool, error) {
		if err != nil {
			return nil, true, errorf("ArithmeticError", "InvalidArgumentValue", "%v", err)
		}
		return res, true, nil
	}
	switch op {
	case "+":
		if lIs && rIs {
			return wrap(temporal.Add(lt, rt))
		}
		return fail()
	case "-":
		if lIs && rIs {
			return wrap(temporal.Sub(lt, rt))
		}
		return fail()
	case "*", "/":
		d, isDur := l.(temporal.Duration)
		if !isDur || rIs {
			if op == "*" {
				if d2, ok := r.(temporal.Duration); ok && isNumber(l) {
					d, isDur, r = d2, true, l
				}
			}
			if !isDur {
				return fail()
			}
		}
		if !isNumber(r) {
			return fail()
		}
		f := toFloat(r)
		if op == "*" {
			return wrap(d.Scale(f))
		}
		res, err := d.Div(f)
		if err != nil {
			return nil, true, errorf("ArithmeticError", "DivisionByZero", "%v", err)
		}
		return res, true, nil
	}
	return fail()
}
