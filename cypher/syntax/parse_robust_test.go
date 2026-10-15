package syntax

import (
	"strings"
	"testing"
	"time"
)

func TestParseNeverPanicsOnSchemaListComprehension(t *testing.T) {
	for _, q := range []string{
		`CREATE FULLTEXT INDEX i FOR (n:A) ON EACH [x IN [1] | x]`,
		`CREATE LOOKUP INDEX i FOR (n) ON EACH [(a)-->(b) | a]`,
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%s: expected a syntax error", q)
		}
	}
}

func TestDeepChainsAreRejected(t *testing.T) {
	rep := func(s string, n int) string {
		b := make([]byte, 0, len(s)*n)
		for i := 0; i < n; i++ {
			b = append(b, s...)
		}
		return string(b)
	}
	for name, q := range map[string]string{
		"plus":   "RETURN 1" + rep("+1", 1000000),
		"prop":   "RETURN a" + rep(".b", 1000000),
		"labels": "MATCH (n" + rep(":A", 1000000) + ") RETURN n",
		"bang":   "RETURN n:" + rep("!", 1000000) + "A",
		"and":    "MATCH (n) WHERE true" + rep(" AND true", 200000) + " RETURN n",
		"isnull": "RETURN a" + rep(" IS NULL", 200000),
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%s: expected a nesting error", name)
		}
	}
	if _, err := Parse("RETURN 1" + rep("+1", 5000)); err != nil {
		t.Errorf("a 5000-term sum must still parse: %v", err)
	}
}

func TestNestedParenMapsParseInLinearTime(t *testing.T) {
	q := "RETURN "
	for i := 0; i < 60; i++ {
		q += "({a:"
	}
	q += "1"
	for i := 0; i < 60; i++ {
		q += "})"
	}
	done := make(chan struct{})
	go func() { _, _ = Parse(q); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parse took exponential time")
	}
}

func TestLongTypeAndShowWordRunsAreLinear(t *testing.T) {
	start := time.Now()
	_, _ = Parse("SHOW " + strings.Repeat("a ", 500000))
	_, _ = Parse("RETURN 1 IS :: " + strings.Repeat("INT ", 500000))
	_, _ = Parse("RETURN '" + strings.Repeat(`\n`, 2000000) + "'")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestNestedPatternPredicateListsAreLinear(t *testing.T) {
	q := "RETURN "
	for i := 0; i < 40; i++ {
		q += "[(a)-->(b {x: "
	}
	q += "1"
	for i := 0; i < 40; i++ {
		q += "})]"
	}
	done := make(chan struct{})
	go func() { _, _ = Parse(q); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parse took exponential time")
	}
}
