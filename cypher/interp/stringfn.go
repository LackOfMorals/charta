package interp

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// fnTrim implements trim(s), btrim(s [, chars]), ltrim(s [, chars]) and
// rtrim(s [, chars]). `trim(BOTH|LEADING|TRAILING chars FROM s)` is parsed
// into btrim/ltrim/rtrim.
func fnTrim(name string, args []any) (any, error) {
	if err := argc(name, args, 1, 2); err != nil {
		return nil, err
	}
	for _, a := range args {
		if a == nil {
			return nil, nil
		}
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, typeErr("%s() expects a string, got %s", name, typeName(args[0]))
	}
	pred := unicode.IsSpace
	if len(args) == 2 {
		chars, ok := args[1].(string)
		if !ok {
			return nil, typeErr("%s() trim characters must be a string, got %s", name, typeName(args[1]))
		}
		pred = func(r rune) bool { return strings.ContainsRune(chars, r) }
	}
	switch name {
	case "ltrim":
		return strings.TrimLeftFunc(s, pred), nil
	case "rtrim":
		return strings.TrimRightFunc(s, pred), nil
	}
	return strings.TrimFunc(s, pred), nil
}

func normForm(name string) (norm.Form, error) {
	switch strings.ToUpper(name) {
	case "", "NFC":
		return norm.NFC, nil
	case "NFD":
		return norm.NFD, nil
	case "NFKC":
		return norm.NFKC, nil
	case "NFKD":
		return norm.NFKD, nil
	}
	return norm.NFC, argErr("unknown normalization form %q (expected NFC, NFD, NFKC or NFKD)", name)
}
