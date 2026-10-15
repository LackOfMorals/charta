package syntax

import "reflect"

// maxASTDepth bounds the depth of a parsed tree. Operator, postfix and label
// chains are parsed by loops into left-deep trees, so the parser's nesting
// counter (maxDepth) does not see them, yet every later pass (analysis,
// evaluation, Dump) recurses once per level. Checking the finished tree keeps
// all of them well inside the goroutine stack.
const maxASTDepth = 10000

// checkDepth fails when the tree below root is deeper than maxASTDepth. It walks
// with an explicit stack, so a very deep tree cannot overflow it.
func (p *parser) checkDepth(root any) {
	type item struct {
		v reflect.Value
		d int
	}
	stack := []item{{reflect.ValueOf(root), 0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		v, d := it.v, it.d
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				stack = append(stack, item{v.Elem(), d})
			}
		case reflect.Pointer:
			if v.IsNil() {
				continue
			}
			d++
			if d > maxASTDepth {
				p.fail(p.toks[0].Pos, "expression is nested too deeply (limit %d)", maxASTDepth)
			}
			stack = append(stack, item{v.Elem(), d})
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				switch v.Field(i).Kind() {
				case reflect.Interface, reflect.Pointer, reflect.Struct, reflect.Slice, reflect.Array:
					stack = append(stack, item{v.Field(i), d})
				}
			}
		case reflect.Slice, reflect.Array:
			switch v.Type().Elem().Kind() {
			case reflect.Interface, reflect.Pointer, reflect.Struct, reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					stack = append(stack, item{v.Index(i), d})
				}
			}
		}
	}
}
