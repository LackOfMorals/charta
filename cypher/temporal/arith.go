package temporal

import (
	"fmt"
	"math/big"
	"time"
)

// Add returns a + b for the supported combinations: temporal + duration,
// duration + temporal, and duration + duration.
func Add(a, b Value) (Value, error) {
	if d, ok := b.(Duration); ok {
		if da, ok := a.(Duration); ok {
			return da.Plus(d)
		}
		return shift(a, d)
	}
	if d, ok := a.(Duration); ok {
		return shift(b, d)
	}
	return nil, fmt.Errorf("cannot add %s and %s", a.Kind().Name(), b.Kind().Name())
}

// Sub returns a - b: temporal - duration or duration - duration.
func Sub(a, b Value) (Value, error) {
	d, ok := b.(Duration)
	if !ok {
		return nil, fmt.Errorf("cannot subtract %s from %s", b.Kind().Name(), a.Kind().Name())
	}
	if da, ok := a.(Duration); ok {
		return da.Minus(d)
	}
	return shift(a, d.Neg())
}

// shift adds a duration to a temporal value. Months move the calendar date
// (clamping to month end), days move the calendar day, and the time part is
// added to the clock. A date has no clock, so whole days from the time part
// carry into it.
func shift(v Value, d Duration) (Value, error) {
	nanos := d.totalNanosBig()
	switch x := v.(type) {
	case Date:
		extraDays := new(big.Int).Quo(nanos, big.NewInt(nsPerDay)) // toward zero
		if !extraDays.IsInt64() {
			return nil, fmt.Errorf("duration is out of range")
		}
		t := addMonths(x.t, d.Months).AddDate(0, 0, int(d.Days+extraDays.Int64()))
		return checkedDate(t)
	case LocalTime:
		return LocalTime{wrapDay(x.ns, nanos)}, nil
	case Time:
		return Time{ns: wrapDay(x.ns, nanos), off: x.off}, nil
	case LocalDateTime:
		t := addMonths(x.t, d.Months).AddDate(0, 0, int(d.Days))
		return LocalDateTime{addBig(t, nanos)}, nil
	case DateTime:
		// Calendar parts move the local date; the time part is exact elapsed time.
		wall := time.Date(x.t.Year(), x.t.Month(), x.t.Day(), x.t.Hour(), x.t.Minute(), x.t.Second(), x.t.Nanosecond(), time.UTC)
		wall = addMonths(wall, d.Months).AddDate(0, 0, int(d.Days))
		moved := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), x.t.Location())
		return DateTime{t: addBig(moved, nanos), named: x.named}, nil
	}
	return nil, fmt.Errorf("cannot add a duration to %s", v.Kind().Name())
}

func checkedDate(t time.Time) (Date, error) {
	if y := t.Year(); y < -999999999 || y > 999999999 {
		return Date{}, fmt.Errorf("year %d is out of range", y)
	}
	return Date{t}, nil
}

// wrapDay adds nanoseconds to a time of day, wrapping at midnight.
func wrapDay(ns int64, delta *big.Int) int64 {
	r := new(big.Int).Mod(delta, big.NewInt(nsPerDay))
	return mod(ns+r.Int64(), nsPerDay)
}

// addBig adds a possibly huge nanosecond count to t in whole-second steps.
func addBig(t time.Time, nanos *big.Int) time.Time {
	secs, rem := new(big.Int).DivMod(nanos, big.NewInt(nsPerSec), new(big.Int))
	return t.Add(time.Duration(secs.Int64()) * time.Second).Add(time.Duration(rem.Int64()))
}

// Equal reports whether two temporal values are equal. Values of different
// kinds are never equal.
func Equal(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch x := a.(type) {
	case Date:
		return x.t.Equal(b.(Date).t)
	case LocalTime:
		return x.ns == b.(LocalTime).ns
	case Time:
		y := b.(Time)
		return x.ns-int64(x.off)*nsPerSec == y.ns-int64(y.off)*nsPerSec && x.off == y.off
	case LocalDateTime:
		return x.t.Equal(b.(LocalDateTime).t)
	case DateTime:
		y := b.(DateTime)
		return x.t.Equal(y.t) && x.Offset() == y.Offset() && x.named == y.named &&
			(!x.named || x.t.Location().String() == y.t.Location().String())
	case Duration:
		return x == b.(Duration)
	}
	return false
}

// Compare orders two values of the same orderable kind. ok is false for
// durations and for values of different kinds.
func Compare(a, b Value) (cmp int, ok bool) {
	if a.Kind() != b.Kind() {
		return 0, false
	}
	switch x := a.(type) {
	case Date:
		return cmpTime(x.t, b.(Date).t), true
	case LocalTime:
		return cmpInt(x.ns, b.(LocalTime).ns), true
	case Time:
		y := b.(Time)
		return cmpInt(x.ns-int64(x.off)*nsPerSec, y.ns-int64(y.off)*nsPerSec), true
	case LocalDateTime:
		return cmpTime(x.t, b.(LocalDateTime).t), true
	case DateTime:
		return cmpTime(x.t, b.(DateTime).t), true
	}
	return 0, false
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
