package temporal

import (
	"fmt"
	"math/big"
)

var (
	ratAvgDaysPerMonth = big.NewRat(3652425, 120000) // 365.2425 / 12
	ratMega            = big.NewRat(1000000, 1)
)

func ratInt(n int64) *big.Rat { return new(big.Rat).SetInt64(n) }

func ratFloat(f float64) *big.Rat {
	r := new(big.Rat)
	if r.SetFloat64(f) == nil {
		return new(big.Rat)
	}
	return r
}

// trunc returns the integer part of r, rounding toward zero, and the rest.
func trunc(r *big.Rat) (*big.Int, *big.Rat) {
	q := new(big.Int).Quo(r.Num(), r.Denom()) // Quo truncates toward zero
	rest := new(big.Rat).Sub(r, new(big.Rat).SetInt(q))
	return q, rest
}

// durParts are the possibly fractional amounts a duration is built from.
type durParts struct {
	years, months, weeks, days                     *big.Rat
	hours, minutes, seconds, millis, micros, nanos *big.Rat
}

func newDurParts() durParts {
	z := func() *big.Rat { return new(big.Rat) }
	return durParts{z(), z(), z(), z(), z(), z(), z(), z(), z(), z()}
}

// build cascades fractions the way Cypher does: a fraction of a month becomes
// days (at the average Gregorian month length), a fraction of a day becomes
// seconds, and sub-nanosecond remainders are dropped.
func (p durParts) build() (Duration, error) {
	months := new(big.Rat).Mul(p.years, ratInt(12))
	months.Add(months, p.months)
	wholeMonths, fracMonths := trunc(months)

	days := new(big.Rat).Mul(p.weeks, ratInt(7))
	days.Add(days, p.days)
	days.Add(days, new(big.Rat).Mul(fracMonths, ratAvgDaysPerMonth))
	wholeDays, fracDays := trunc(days)

	nanos := new(big.Rat).Mul(fracDays, ratInt(86400*nsPerSec))
	nanos.Add(nanos, new(big.Rat).Mul(p.hours, ratInt(3600*nsPerSec)))
	nanos.Add(nanos, new(big.Rat).Mul(p.minutes, ratInt(60*nsPerSec)))
	nanos.Add(nanos, new(big.Rat).Mul(p.seconds, ratInt(nsPerSec)))
	nanos.Add(nanos, new(big.Rat).Mul(p.millis, ratInt(1000000)))
	nanos.Add(nanos, new(big.Rat).Mul(p.micros, ratInt(1000)))
	nanos.Add(nanos, p.nanos)
	// Absorb binary floating point noise (0.3 is stored as 0.29999...) before
	// truncating to whole nanoseconds.
	scaled := new(big.Rat).Mul(nanos, ratMega)
	rounded := new(big.Int).Quo(new(big.Int).Add(new(big.Int).Mul(scaled.Num(), big.NewInt(2)), signed(scaled.Denom(), scaled.Sign())), new(big.Int).Mul(scaled.Denom(), big.NewInt(2)))
	totalNanos := new(big.Int).Quo(rounded, big.NewInt(1000000))

	if !wholeMonths.IsInt64() || !wholeDays.IsInt64() {
		return Duration{}, fmt.Errorf("duration is out of range")
	}
	secs, rem := new(big.Int).DivMod(totalNanos, big.NewInt(nsPerSec), new(big.Int)) // floor
	if !secs.IsInt64() {
		return Duration{}, fmt.Errorf("duration is out of range")
	}
	return Duration{Months: wholeMonths.Int64(), Days: wholeDays.Int64(), Seconds: secs.Int64(), Nanos: rem.Int64()}, nil
}

func signed(n *big.Int, sign int) *big.Int {
	if sign < 0 {
		return new(big.Int).Neg(n)
	}
	return n
}

// totalNanos returns the time part of d in nanoseconds (may overflow only for
// absurd durations, which the callers bound).
func (d Duration) totalNanosBig() *big.Int {
	n := new(big.Int).Mul(big.NewInt(d.Seconds), big.NewInt(nsPerSec))
	return n.Add(n, big.NewInt(d.Nanos))
}

func durationFromNanos(months, days int64, nanos *big.Int) (Duration, error) {
	secs, rem := new(big.Int).DivMod(nanos, big.NewInt(nsPerSec), new(big.Int))
	if !secs.IsInt64() {
		return Duration{}, fmt.Errorf("duration is out of range")
	}
	return Duration{Months: months, Days: days, Seconds: secs.Int64(), Nanos: rem.Int64()}, nil
}

// Neg returns -d.
func (d Duration) Neg() Duration {
	n, _ := durationFromNanos(-d.Months, -d.Days, new(big.Int).Neg(d.totalNanosBig()))
	return n
}

// Plus returns d + o.
func (d Duration) Plus(o Duration) (Duration, error) {
	return durationFromNanos(d.Months+o.Months, d.Days+o.Days, new(big.Int).Add(d.totalNanosBig(), o.totalNanosBig()))
}

// Minus returns d - o.
func (d Duration) Minus(o Duration) (Duration, error) { return d.Plus(o.Neg()) }

// Scale multiplies d by f, cascading fractional months and days like the
// constructors do.
func (d Duration) Scale(f float64) (Duration, error) {
	r := ratFloat(f)
	p := newDurParts()
	p.months.Mul(ratInt(d.Months), r)
	p.days.Mul(ratInt(d.Days), r)
	p.nanos.Mul(new(big.Rat).SetInt(d.totalNanosBig()), r)
	return p.build()
}

// Div divides d by f.
func (d Duration) Div(f float64) (Duration, error) {
	if f == 0 {
		return Duration{}, fmt.Errorf("division by zero")
	}
	r := ratFloat(f)
	r.Inv(r)
	p := newDurParts()
	p.months.Mul(ratInt(d.Months), r)
	p.days.Mul(ratInt(d.Days), r)
	p.nanos.Mul(new(big.Rat).SetInt(d.totalNanosBig()), r)
	return p.build()
}
