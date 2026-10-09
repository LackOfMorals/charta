package temporal_test

import (
	"testing"

	"github.com/LackOfMorals/graphlite/v2/cypher/temporal"
)

func str(t *testing.T, k temporal.Kind, arg any) string {
	t.Helper()
	v, err := temporal.Construct(k, arg)
	if err != nil {
		t.Fatalf("%s(%v): %v", k.Name(), arg, err)
	}
	return v.String()
}

func TestParseAndRender(t *testing.T) {
	tests := []struct {
		kind temporal.Kind
		in   string
		want string
	}{
		{temporal.KindDate, "2015-07-21", "2015-07-21"},
		{temporal.KindDate, "20150721", "2015-07-21"},
		{temporal.KindDate, "2015-07", "2015-07-01"},
		{temporal.KindDate, "2015-W30-2", "2015-07-21"},
		{temporal.KindDate, "2015W302", "2015-07-21"},
		{temporal.KindDate, "2015-W30", "2015-07-20"},
		{temporal.KindDate, "2015-202", "2015-07-21"},
		{temporal.KindDate, "2015", "2015-01-01"},
		{temporal.KindLocalTime, "214032.142", "21:40:32.142"},
		{temporal.KindLocalTime, "21", "21:00"},
		{temporal.KindTime, "21:40:32.142+0100", "21:40:32.142+01:00"},
		{temporal.KindTime, "2140-00:00", "21:40Z"},
		{temporal.KindTime, "22+18:00", "22:00+18:00"},
		{temporal.KindLocalDateTime, "2015-W30-2T214032.142", "2015-07-21T21:40:32.142"},
		{temporal.KindLocalDateTime, "2015T214032", "2015-01-01T21:40:32"},
		{temporal.KindDateTime, "2015-07-21T21:40:32.142+0100", "2015-07-21T21:40:32.142+01:00"},
		{temporal.KindDateTime, "2015-07-21T21:40:32.142+02:00[Europe/Stockholm]", "2015-07-21T21:40:32.142+02:00[Europe/Stockholm]"},
		{temporal.KindDateTime, "2015-07-21T21:40:32.142[Europe/London]", "2015-07-21T21:40:32.142+01:00[Europe/London]"},
		{temporal.KindDuration, "P14DT16H12M", "P14DT16H12M"},
		{temporal.KindDuration, "P5M1.5D", "P5M1DT12H"},
		{temporal.KindDuration, "P0.75M", "P22DT19H51M49.5S"},
		{temporal.KindDuration, "PT0.75M", "PT45S"},
		{temporal.KindDuration, "P2.5W", "P17DT12H"},
		{temporal.KindDuration, "P12Y5M14DT16H12M70S", "P12Y5M14DT16H13M10S"},
		{temporal.KindDuration, "P2012-02-02T14:37:21.545", "P2012Y2M2DT14H37M21.545S"},
	}
	for _, tt := range tests {
		if got := str(t, tt.kind, tt.in); got != tt.want {
			t.Errorf("%s(%q) = %s, want %s", tt.kind.Name(), tt.in, got, tt.want)
		}
	}
}

func TestMaps(t *testing.T) {
	m := func(kv ...any) map[string]any {
		out := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}
	i := func(n int) int64 { return int64(n) }
	tests := []struct {
		kind temporal.Kind
		arg  map[string]any
		want string
	}{
		{temporal.KindDate, m("year", i(1816), "week", i(1)), "1816-01-01"},
		{temporal.KindDate, m("year", i(1818), "week", i(53)), "1818-12-28"},
		{temporal.KindDate, m("year", i(1984), "quarter", i(3), "dayOfQuarter", i(45)), "1984-08-14"},
		{temporal.KindDate, m("year", i(1984), "ordinalDay", i(202)), "1984-07-20"},
		{temporal.KindLocalTime, m("hour", i(12), "minute", i(31), "second", i(14), "nanosecond", i(789), "millisecond", i(123), "microsecond", i(456)), "12:31:14.123456789"},
		{temporal.KindTime, m("hour", i(12), "timezone", "+01:00"), "12:00+01:00"},
		{temporal.KindDateTime, m("year", i(1984), "month", i(10), "day", i(11), "timezone", "Europe/Stockholm"), "1984-10-11T00:00+01:00[Europe/Stockholm]"},
		{temporal.KindDuration, m("months", 0.75), "P22DT19H51M49.5S"},
		{temporal.KindDuration, m("seconds", i(-60), "milliseconds", i(-1)), "PT-1M-0.001S"},
		{temporal.KindDuration, m("seconds", i(2), "milliseconds", i(-1)), "PT1.999S"},
		{temporal.KindDuration, m("years", 12.5, "months", 5.5, "days", 14.5, "hours", 16.5, "minutes", 12.5, "seconds", 70.5, "nanoseconds", i(3)), "P12Y11M29DT33H58M13.500000003S"},
	}
	for _, tt := range tests {
		if got := str(t, tt.kind, tt.arg); got != tt.want {
			t.Errorf("%s(%v) = %s, want %s", tt.kind.Name(), tt.arg, got, tt.want)
		}
	}
}

func TestArithmetic(t *testing.T) {
	d := func(s string) temporal.Value { v, _ := temporal.Construct(temporal.KindDate, s); return v }
	dur := func(s string) temporal.Value { v, _ := temporal.Construct(temporal.KindDuration, s); return v }
	sum, err := temporal.Add(d("1984-10-11"), dur("P12Y5M14DT16H12M70S"))
	if err != nil || sum.String() != "1997-03-25" {
		t.Errorf("date + duration = %v, %v", sum, err)
	}
	diff, err := temporal.Sub(d("1984-10-11"), dur("P12Y5M14DT16H12M70S"))
	if err != nil || diff.String() != "1972-04-27" {
		t.Errorf("date - duration = %v, %v", diff, err)
	}
}

func TestBetween(t *testing.T) {
	c := func(k temporal.Kind, s string) temporal.Value {
		v, err := temporal.Construct(k, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		op   string
		a, b temporal.Value
		want string
	}{
		{"between", c(temporal.KindDate, "1984-10-11"), c(temporal.KindDate, "2015-06-24"), "P30Y8M13D"},
		{"between", c(temporal.KindLocalDateTime, "2018-01-02T10:00:00.1"), c(temporal.KindLocalDateTime, "2018-01-01T10:00:00.2"), "PT-23H-59M-59.9S"},
		{"between", c(temporal.KindDateTime, "2017-10-28T23:00+02:00[Europe/Stockholm]"), c(temporal.KindDateTime, "2017-10-29T04:00+01:00[Europe/Stockholm]"), "PT6H"},
		{"inMonths", c(temporal.KindDate, "2018-03-11"), c(temporal.KindDate, "2016-06-24"), "P-1Y-8M"},
		{"inDays", c(temporal.KindDate, "1984-10-11"), c(temporal.KindDate, "2015-06-24"), "P11213D"},
		{"inSeconds", c(temporal.KindLocalTime, "12:34:56.3"), c(temporal.KindLocalTime, "12:34:54.7"), "PT-1.6S"},
		{"between", c(temporal.KindDate, "-999999999-01-01"), c(temporal.KindDate, "+999999999-12-31"), "P1999999998Y11M30D"},
	}
	for _, tt := range tests {
		got, err := temporal.Between(tt.op, tt.a, tt.b)
		if err != nil || got.String() != tt.want {
			t.Errorf("duration.%s(%v, %v) = %v (%v), want %s", tt.op, tt.a, tt.b, got, err, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	dt, _ := temporal.Construct(temporal.KindDateTime, "2017-10-11T12:31:14.645876123-01:00")
	got, err := temporal.Truncate(temporal.KindDateTime, "millennium", dt, map[string]any{"timezone": "Europe/Stockholm"})
	if err != nil || got.String() != "2000-01-01T00:00+01:00[Europe/Stockholm]" {
		t.Errorf("got %v, %v", got, err)
	}
	d, _ := temporal.Construct(temporal.KindDate, "1984-02-01")
	got, err = temporal.Truncate(temporal.KindDate, "weekYear", d, map[string]any{"day": int64(5)})
	if err != nil || got.String() != "1984-01-05" {
		t.Errorf("got %v, %v", got, err)
	}
}
