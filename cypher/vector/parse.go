package vector

import (
	"math"
	"strconv"
)

// pow10 holds the powers of ten a double represents exactly.
var pow10 = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11,
	1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}

// parseNumber reads the JSON number starting at s[i] and returns its value and
// the index after it. ok is false if s[i:] does not start with a finite number.
//
// Up to 19 significant digits are kept; further ones are dropped, which is
// harmless here because the value ends up in a float32 (24 bits, about 7
// decimal digits). The result is the exact double for numbers of at most 15
// digits and within a couple of units in the last place of it otherwise, where
// the classic fast path would give up above 15 digits and a stored embedding is
// typically written with 17 (strconv then costs 45 ns a number; this about 8).
// Decimal exponents outside +-22 go to strconv.ParseFloat.
func parseNumber(s string, i int) (v float64, next int, ok bool) {
	start := i
	neg := false
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	var mant uint64
	digits, frac, dropped := 0, 0, 0
	sawDigit, sawDot := false, false
	for i < len(s) {
		c := s[i]
		if c >= '0' && c <= '9' {
			sawDigit = true
			if digits < 19 {
				mant = mant*10 + uint64(c-'0')
				if mant != 0 {
					digits++
				}
				if sawDot {
					frac++
				}
			} else if !sawDot {
				dropped++
			}
			i++
		} else if c == '.' && !sawDot {
			sawDot = true
			i++
		} else {
			break
		}
	}
	if !sawDigit {
		return 0, start, false
	}
	exp := dropped - frac
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		eneg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			eneg = s[i] == '-'
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return 0, start, false
		}
		e := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if e < 10000 {
				e = e*10 + int(s[i]-'0')
			}
			i++
		}
		if eneg {
			e = -e
		}
		exp += e
	}
	if mant == 0 {
		if neg {
			return math.Copysign(0, -1), i, true
		}
		return 0, i, true
	}
	if exp < -22 || exp > 22 {
		return slowNumber(s, start)
	}
	f := float64(mant)
	switch {
	case exp > 0:
		f *= pow10[exp]
	case exp < 0:
		f /= pow10[-exp]
	}
	if neg {
		f = -f
	}
	return f, i, true
}

// slowNumber parses the number at s[start:] with strconv.
func slowNumber(s string, start int) (float64, int, bool) {
	end := start
	if end < len(s) && s[end] == '-' {
		end++
	}
	for end < len(s) {
		c := s[end]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			end++
			continue
		}
		break
	}
	f, err := strconv.ParseFloat(s[start:end], 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, start, false
	}
	return f, end, true
}

// ParseFloat32s parses the JSON array of numbers at the start of text into dst,
// which must have the vector's dimension as its length. ok is false if it is not
// an array of exactly len(dst) finite numbers. Anything after the closing
// bracket is ignored.
func ParseFloat32s(text string, dst []float32) (ok bool) {
	i := 0
	skip := func() {
		for i < len(text) && (text[i] == ' ' || text[i] == '\n' || text[i] == '\t' || text[i] == '\r') {
			i++
		}
	}
	skip()
	if i >= len(text) || text[i] != '[' {
		return false
	}
	i++
	for k := range dst {
		skip()
		v, next, good := parseNumber(text, i)
		if !good {
			return false
		}
		f := float32(v)
		if math.IsInf(float64(f), 0) {
			return false
		}
		dst[k] = f
		i = next
		skip()
		if i >= len(text) {
			return false
		}
		if k < len(dst)-1 {
			if text[i] != ',' {
				return false
			}
			i++
		}
	}
	skip()
	return i < len(text) && text[i] == ']'
}

// skipValue returns the index just past the JSON value that starts at s[i]
// (after any whitespace), or -1 if it is malformed. Strings are skipped with
// their escapes, arrays and objects by bracket depth.
func skipValue(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\n' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	if i >= len(s) {
		return -1
	}
	switch s[i] {
	case '"':
		i++
		for i < len(s) {
			switch s[i] {
			case '\\':
				i += 2
			case '"':
				return i + 1
			default:
				i++
			}
		}
		return -1
	case '[', '{':
		depth := 0
		for i < len(s) {
			switch s[i] {
			case '"':
				if i = skipValue(s, i); i < 0 {
					return -1
				}
				continue
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return -1
	}
	for i < len(s) && s[i] != ',' && s[i] != '}' && s[i] != ']' && s[i] != ' ' && s[i] != '\n' && s[i] != '\t' && s[i] != '\r' {
		i++
	}
	return i
}

// objectMember returns the text of the member named key in the JSON object
// text, starting at its value, without decoding anything else in it. The
// returned text runs to the end of text, not the end of the value: the caller
// reads one value from its start (so the value itself is traversed only once).
func objectMember(text, key string) (string, bool) {
	i := 0
	for i < len(text) && (text[i] == ' ' || text[i] == '\n' || text[i] == '\t' || text[i] == '\r') {
		i++
	}
	if i >= len(text) || text[i] != '{' {
		return "", false
	}
	i++
	for {
		for i < len(text) && (text[i] == ' ' || text[i] == '\n' || text[i] == '\t' || text[i] == '\r' || text[i] == ',') {
			i++
		}
		if i >= len(text) || text[i] == '}' {
			return "", false
		}
		if text[i] != '"' {
			return "", false
		}
		end := skipValue(text, i)
		if end < 0 {
			return "", false
		}
		name := text[i+1 : end-1]
		i = end
		for i < len(text) && (text[i] == ' ' || text[i] == '\n' || text[i] == '\t' || text[i] == '\r') {
			i++
		}
		if i >= len(text) || text[i] != ':' {
			return "", false
		}
		i++
		for i < len(text) && (text[i] == ' ' || text[i] == '\n' || text[i] == '\t' || text[i] == '\r') {
			i++
		}
		if name == key { // keys here are plain identifiers, so no escapes to undo
			return text[i:], true
		}
		valEnd := skipValue(text, i)
		if valEnd < 0 {
			return "", false
		}
		i = valEnd
	}
}

// StoredVector extracts the coordinates of the property named key from the
// JSON text of a node's properties, which hold either a plain list [1, 2, 3] or
// a VECTOR value {"$v":"FLOAT32","c":[1, 2, 3]}, and parses them into dst. It
// reads only that one property: the rest of the document is skipped, not
// decoded. ok is false if the property is absent or is not a vector of
// len(dst) numbers.
func StoredVector(props, key string, dst []float32) (ok bool) {
	val, found := objectMember(props, key)
	if !found {
		return false
	}
	if len(val) > 0 && val[0] == '{' {
		if val, found = objectMember(val, "c"); !found {
			return false
		}
	}
	return ParseFloat32s(val, dst)
}
