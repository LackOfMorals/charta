package api

import (
	"testing"

	"github.com/graingraph/graingraph/cypher/temporal"
)

// The expectations were taken from a Neo4j 2026.07 server.
func TestTemporalString(t *testing.T) {
	parse := func(kind, s string) temporal.Value {
		t.Helper()
		k, ok := temporal.KindByName(kind)
		if !ok {
			t.Fatalf("unknown kind %q", kind)
		}
		v, err := temporal.Parse(k, s)
		if err != nil {
			t.Fatalf("parse %s %q: %v", kind, s, err)
		}
		return v
	}
	for _, tc := range []struct {
		kind, in         string
		wantPlain, typed string
	}{
		{"localtime", "12:30", "12:30:00", "12:30:00"},
		{"localtime", "12:30:15.5", "12:30:15.5", "12:30:15.5"},
		{"localtime", "12:30:15.123456789", "12:30:15.123456789", "12:30:15.123456789"},
		{"time", "12:30+01:00", "12:30:00+01:00", "12:30:00+01:00"},
		{"time", "12:30:15.25-05:30", "12:30:15.25-05:30", "12:30:15.25-05:30"},
		{"localdatetime", "2024-03-15T10:30", "2024-03-15T10:30:00", "2024-03-15T10:30:00"},
		{"datetime", "2024-03-15T10:30Z", "2024-03-15T10:30:00Z", "2024-03-15T10:30:00Z"},
		{"datetime", "2024-03-15T10:30:45.5+01:00", "2024-03-15T10:30:45.5+01:00", "2024-03-15T10:30:45.5+01:00"},
		// A region zone is kept by Typed JSON only.
		{"datetime", "2024-03-15T10:30[Europe/Stockholm]", "2024-03-15T10:30:00+01:00", "2024-03-15T10:30:00+01:00[Europe/Stockholm]"},
		{"datetime", "2024-01-15T10:30[Etc/GMT+5]", "2024-01-15T10:30:00-05:00", "2024-01-15T10:30:00-05:00[Etc/GMT+5]"},
		{"date", "2024-03-15", "2024-03-15", "2024-03-15"},
	} {
		v := parse(tc.kind, tc.in)
		if got := temporalString(v, false); got != tc.wantPlain {
			t.Errorf("%s %q plain = %q, want %q", tc.kind, tc.in, got, tc.wantPlain)
		}
		if got := temporalString(v, true); got != tc.typed {
			t.Errorf("%s %q typed = %q, want %q", tc.kind, tc.in, got, tc.typed)
		}
	}
}
