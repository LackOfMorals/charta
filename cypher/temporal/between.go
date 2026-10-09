package temporal

import (
	"fmt"
	"math/big"
	"time"
)

// operand is a temporal value reduced to what duration.between needs.
type operand struct {
	date  *Date
	clock int64 // nanoseconds since midnight (0 when the value has no time)
	zone  *zoneSpec
}

func operandOf(v Value) (operand, error) {
	if v.Kind() == KindDuration {
		return operand{}, fmt.Errorf("duration.between needs temporal values, not durations")
	}
	p := partsOf(v)
	o := operand{date: p.date, zone: p.zone}
	if p.time != nil {
		o.clock = p.time.ns
	}
	return o, nil
}

// Between computes duration.between (op "between"), duration.inMonths,
// duration.inDays or duration.inSeconds of two temporal values.
func Between(op string, a, b Value) (Duration, error) {
	oa, err := operandOf(a)
	if err != nil {
		return Duration{}, err
	}
	ob, err := operandOf(b)
	if err != nil {
		return Duration{}, err
	}
	// A value without a date borrows the other's; with neither, any date does.
	fallback := Date{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	switch {
	case oa.date == nil && ob.date != nil:
		oa.date = ob.date
	case ob.date == nil && oa.date != nil:
		ob.date = oa.date
	case oa.date == nil && ob.date == nil:
		oa.date, ob.date = &fallback, &fallback
	}
	// Zone: the first operand's if it has one, otherwise the second's.
	zone := oa.zone
	if zone == nil {
		zone = ob.zone
	}
	localA := combineLocal(*oa.date, LocalTime{oa.clock}).t
	localB := combineLocal(*ob.date, LocalTime{ob.clock}).t
	if zone == nil {
		return betweenLocal(op, localA, localB, utc)
	}
	// Both zoned: express b in a's zone; a lone zone applies to both.
	if oa.zone != nil && ob.zone != nil {
		instB := time.Date(localB.Year(), localB.Month(), localB.Day(), localB.Hour(), localB.Minute(), localB.Second(), localB.Nanosecond(), ob.zone.loc)
		localB = instB.In(zone.loc)
		localB = time.Date(localB.Year(), localB.Month(), localB.Day(), localB.Hour(), localB.Minute(), localB.Second(), localB.Nanosecond(), time.UTC)
	}
	return betweenLocal(op, localA, localB, *zone)
}

// betweenLocal works on wall-clock times (labelled UTC) that both live in zone.
func betweenLocal(op string, la, lb time.Time, zone zoneSpec) (Duration, error) {
	inst := func(wall time.Time) time.Time {
		return time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), zone.loc)
	}
	seconds := func(from, to time.Time) (Duration, error) {
		d := new(big.Int).Sub(bigNanos(to), bigNanos(from))
		return durationFromNanos(0, 0, d)
	}
	switch op {
	case "inSeconds":
		return seconds(inst(la), inst(lb))
	case "inMonths":
		return Duration{Months: wholeMonths(la, lb)}, nil
	case "inDays":
		return Duration{Days: wholeDays(la, lb)}, nil
	}
	months := wholeMonths(la, lb)
	mid := addMonthsUnclamped(la, months)
	days := wholeDays(mid, lb)
	mid = mid.AddDate(0, 0, int(days))
	rest, err := seconds(inst(mid), inst(lb))
	if err != nil {
		return Duration{}, err
	}
	rest.Months, rest.Days = months, days
	return rest, nil
}

func bigNanos(t time.Time) *big.Int {
	n := new(big.Int).Mul(big.NewInt(t.Unix()), big.NewInt(nsPerSec))
	return n.Add(n, big.NewInt(int64(t.Nanosecond())))
}

// clockOf is the time of day in nanoseconds.
func clockOf(t time.Time) int64 { return wallNanos(t) }

// wholeMonths counts the complete months from a to b, toward zero.
func wholeMonths(a, b time.Time) int64 {
	end := adjustEnd(a, b)
	packed := func(t time.Time) int64 { return (int64(t.Year())*12+int64(t.Month()-1))*32 + int64(t.Day()) }
	return (packed(end) - packed(a)) / 32 // Go truncates toward zero, like Java
}

// wholeDays counts the complete days from a to b, toward zero.
func wholeDays(a, b time.Time) int64 {
	end := adjustEnd(a, b)
	return dayNumber(end) - dayNumber(a)
}

// adjustEnd moves b's date one day toward a when b's time of day has not yet
// reached a's, so partial days and months are not counted.
func adjustEnd(a, b time.Time) time.Time {
	da, db := dayNumber(a), dayNumber(b)
	switch {
	case db > da && clockOf(b) < clockOf(a):
		return b.AddDate(0, 0, -1)
	case db < da && clockOf(b) > clockOf(a):
		return b.AddDate(0, 0, 1)
	}
	return b
}

// dayNumber is the number of days since the Unix epoch for the wall-clock date.
func dayNumber(t time.Time) int64 {
	return floorDiv(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix(), 86400)
}

// addMonthsUnclamped moves a wall-clock time by whole months, clamping the day
// to the end of the target month (LocalDateTime.plusMonths).
func addMonthsUnclamped(t time.Time, months int64) time.Time { return addMonths(t, months) }
