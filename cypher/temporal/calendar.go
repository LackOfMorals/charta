package temporal

import (
	"fmt"
	"time"
)

// isoWeekMonday returns the Monday of ISO week 1 of the given week-year.
func isoWeek1Monday(weekYear int) time.Time {
	jan4 := time.Date(weekYear, time.January, 4, 0, 0, 0, 0, time.UTC)
	back := (int(jan4.Weekday()) + 6) % 7 // days since Monday
	return jan4.AddDate(0, 0, -back)
}

// weeksInYear is 52 or 53.
func weeksInYear(weekYear int) int {
	_, w := time.Date(weekYear, time.December, 28, 0, 0, 0, 0, time.UTC).ISOWeek()
	return w
}

func dateFromWeek(weekYear, week, dow int) (Date, error) {
	if week < 1 || week > weeksInYear(weekYear) {
		return Date{}, fmt.Errorf("week %d is out of range for week-year %d", week, weekYear)
	}
	if dow < 1 || dow > 7 {
		return Date{}, fmt.Errorf("day of week %d is out of range", dow)
	}
	return Date{isoWeek1Monday(weekYear).AddDate(0, 0, (week-1)*7+dow-1)}, nil
}

func dateFromOrdinal(year, ordinal int) (Date, error) {
	n := 365
	if daysIn(year, 2) == 29 {
		n = 366
	}
	if ordinal < 1 || ordinal > n {
		return Date{}, fmt.Errorf("ordinal day %d is out of range for %d", ordinal, year)
	}
	return Date{time.Date(year, time.January, ordinal, 0, 0, 0, 0, time.UTC)}, nil
}

func quarterOf(month int) int { return (month-1)/3 + 1 }

func dateFromQuarter(year, quarter, dayOfQuarter int) (Date, error) {
	if quarter < 1 || quarter > 4 {
		return Date{}, fmt.Errorf("quarter %d is out of range", quarter)
	}
	first := time.Date(year, time.Month((quarter-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
	end := first.AddDate(0, 3, 0)
	days := int(end.Sub(first).Hours() / 24)
	if dayOfQuarter < 1 || dayOfQuarter > days {
		return Date{}, fmt.Errorf("day of quarter %d is out of range", dayOfQuarter)
	}
	return Date{first.AddDate(0, 0, dayOfQuarter-1)}, nil
}

// Date components.

func (d Date) Year() int    { return d.t.Year() }
func (d Date) Month() int   { return int(d.t.Month()) }
func (d Date) Day() int     { return d.t.Day() }
func (d Date) Quarter() int { return quarterOf(d.Month()) }
func (d Date) Ordinal() int { return d.t.YearDay() }

// Week is the ISO week number.
func (d Date) Week() int { _, w := d.t.ISOWeek(); return w }

// WeekYear is the ISO week-numbering year.
func (d Date) WeekYear() int { y, _ := d.t.ISOWeek(); return y }

// WeekDay is the ISO day of the week, Monday = 1 … Sunday = 7.
func (d Date) WeekDay() int { return (int(d.t.Weekday())+6)%7 + 1 }

// DayOfQuarter is the 1-based day within the quarter.
func (d Date) DayOfQuarter() int {
	first := time.Date(d.Year(), time.Month((d.Quarter()-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
	return int(d.t.Sub(first).Hours()/24) + 1
}

// addMonths adds months, clamping the day to the end of the month.
func addMonths(t time.Time, months int64) time.Time {
	y, m, d := t.Date()
	total := int64(y)*12 + int64(m-1) + months
	ny := int(floorDiv(total, 12))
	nm := int(total - int64(ny)*12)
	if dim := daysIn(ny, nm+1); d > dim {
		d = dim
	}
	return time.Date(ny, time.Month(nm+1), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
