package syntax

import (
	"reflect"
	"strconv"
	"strings"
)

// Dump renders an AST as a compact single-line s-expression, omitting
// positions and zero-valued fields. It is intended for golden tests and
// debugging, e.g.
//
//	(Comparison Operands:[(Ident Name:"a") (IntLit Value:1 Text:"1")] Ops:["<"])
func Dump(n Node) string {
	var sb strings.Builder
	dumpValue(&sb, reflect.ValueOf(n))
	return sb.String()
}

var locType = reflect.TypeOf(Loc{})

func dumpValue(sb *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Invalid:
		sb.WriteString("nil")
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			sb.WriteString("nil")
			return
		}
		dumpValue(sb, v.Elem())
	case reflect.Struct:
		sb.WriteByte('(')
		sb.WriteString(v.Type().Name())
		dumpFields(sb, v)
		sb.WriteByte(')')
	case reflect.Slice:
		sb.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				sb.WriteByte(' ')
			}
			dumpValue(sb, v.Index(i))
		}
		sb.WriteByte(']')
	case reflect.String:
		sb.WriteString(strconv.Quote(v.String()))
	case reflect.Bool:
		sb.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		sb.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sb.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		sb.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		sb.WriteString(v.String())
	}
}

// dumpFields writes " Name:value" for each non-zero field, flattening embedded
// structs other than Loc (which is omitted).
func dumpFields(sb *strings.Builder, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type == locType {
			continue
		}
		fv := v.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			dumpFields(sb, fv)
			continue
		}
		if fv.IsZero() {
			continue
		}
		sb.WriteByte(' ')
		sb.WriteString(f.Name)
		sb.WriteByte(':')
		dumpValue(sb, fv)
	}
}
