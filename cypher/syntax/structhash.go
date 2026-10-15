package syntax

import (
	"math"
	"reflect"
)

// Structural hashing and equality of AST nodes, ignoring source positions.
// They replace comparing Dump strings, which re-serialises a whole subtree on
// every comparison (quadratic when done per node of a long expression).

// Hasher computes structural hashes, memoising each node by address so hashing
// every node of a tree costs linear time in total. A Hasher is not safe for
// concurrent use and must not outlive the tree it hashed.
type Hasher struct{ memo map[uintptr]hashed }

// hashed keeps the node alive so its address cannot be reused by another node
// while the memo entry exists.
type hashed struct {
	h    uint64
	keep reflect.Value
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func mix(h, x uint64) uint64 { return (h ^ x) * fnvPrime }

// Hash returns the structural hash of n; equal trees hash equally.
func (h *Hasher) Hash(n Node) uint64 {
	if h.memo == nil {
		h.memo = map[uintptr]hashed{}
	}
	return h.value(fnvOffset, reflect.ValueOf(n))
}

func (h *Hasher) value(acc uint64, v reflect.Value) uint64 {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return mix(acc, 0x9e37)
		}
		return h.value(acc, v.Elem())
	case reflect.Pointer:
		if v.IsNil() {
			return mix(acc, 0x9e37)
		}
		key := v.Pointer()
		if e, ok := h.memo[key]; ok {
			return mix(acc, e.h)
		}
		sub := h.value(fnvOffset, v.Elem())
		h.memo[key] = hashed{sub, v}
		return mix(acc, sub)
	case reflect.Struct:
		t := v.Type()
		acc = mix(acc, uint64(len(t.Name()))+0x51)
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).Type == locType {
				continue
			}
			acc = h.value(acc, v.Field(i))
		}
		return acc
	case reflect.Slice, reflect.Array:
		acc = mix(acc, uint64(v.Len())+0x77)
		for i := 0; i < v.Len(); i++ {
			acc = h.value(acc, v.Index(i))
		}
		return acc
	case reflect.String:
		s := v.String()
		acc = mix(acc, uint64(len(s)))
		for i := 0; i < len(s); i++ {
			acc = mix(acc, uint64(s[i]))
		}
		return acc
	case reflect.Bool:
		if v.Bool() {
			return mix(acc, 1)
		}
		return mix(acc, 2)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return mix(acc, uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return mix(acc, v.Uint())
	case reflect.Float32, reflect.Float64:
		return mix(acc, math.Float64bits(v.Float()))
	}
	return acc
}

// Equal reports whether two nodes are structurally identical, ignoring
// positions. It stops at the first difference.
func Equal(a, b Node) bool {
	return equalValue(reflect.ValueOf(a), reflect.ValueOf(b))
}

func equalValue(a, b reflect.Value) bool {
	if a.Kind() != b.Kind() || a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		if a.Kind() == reflect.Pointer && a.Pointer() == b.Pointer() {
			return true
		}
		return equalValue(a.Elem(), b.Elem())
	case reflect.Struct:
		t := a.Type()
		for i := 0; i < a.NumField(); i++ {
			if t.Field(i).Type == locType {
				continue
			}
			if !equalValue(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !equalValue(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.String:
		return a.String() == b.String()
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		return math.Float64bits(a.Float()) == math.Float64bits(b.Float())
	}
	return true
}
