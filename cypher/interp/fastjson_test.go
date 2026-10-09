package interp

import (
	"reflect"
	"testing"
)

// The fast decoder must agree with the encoding/json path on everything the
// store can contain.
func TestFastDecodeMatchesSlow(t *testing.T) {
	for _, in := range []string{
		`{}`,
		`{"a":1,"b":2.5,"c":"x","d":true,"e":false,"f":null}`,
		`{"n":-3,"f":1.0,"big":9223372036854775807,"over":9223372036854775808,"exp":1e3}`,
		`{"l":[1,2.0,"s",[],{"k":[null]}],"m":{"x":{"y":1}}}`,
		`{"s":"quote \" backslash \\ newline \n unicode \u00e9 é"}`,
		` { "sp" : [ 1 , 2 ] } `,
		`{"empty":"","k with space":1}`,
	} {
		fast, ok := fastDecodeObject(in)
		if !ok {
			t.Errorf("fast decoder declined %s", in)
			continue
		}
		slow, err := decodePropsSlow(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !reflect.DeepEqual(fast, slow) {
			t.Errorf("%s:\n fast %#v\n slow %#v", in, fast, slow)
		}
	}
	for _, bad := range []string{`{`, `{"a"}`, `{"a":}`, `[1]`, `{"a":1,}`, `{"a":tru}`, `{"a":1} x`} {
		if _, ok := fastDecodeObject(bad); ok {
			t.Errorf("fast decoder accepted invalid %s", bad)
		}
	}
}
