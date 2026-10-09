// Package temporal implements the Cypher temporal types: DATE, LOCAL TIME,
// ZONED TIME, LOCAL DATETIME, ZONED DATETIME and DURATION. It covers
// construction (from strings, maps and other temporal values), component
// access, truncation, arithmetic, comparison, duration.between and the
// canonical string rendering that Cypher uses for output and storage.
//
// The package depends only on the standard library.
package temporal

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // named time zones must work without a system zoneinfo
)

// Kind identifies a temporal type.
type Kind int

const (
	KindDate Kind = iota + 1
	KindLocalTime
	KindTime
	KindLocalDateTime
	KindDateTime
	KindDuration
)

// Name is the Cypher function name that constructs the kind.
func (k Kind) Name() string {
	switch k {
	case KindDate:
		return "date"
	case KindLocalTime:
		return "localtime"
	case KindTime:
		return "time"
	case KindLocalDateTime:
		return "localdatetime"
	case KindDateTime:
		return "datetime"
	case KindDuration:
		return "duration"
	}
	return "?"
}

// KindByName returns the kind a constructor function name refers to.
func KindByName(name string) (Kind, bool) {
	switch strings.ToLower(name) {
	case "date":
		return KindDate, true
	case "localtime":
		return KindLocalTime, true
	case "time":
		return KindTime, true
	case "localdatetime":
		return KindLocalDateTime, true
	case "datetime":
		return KindDateTime, true
	case "duration":
		return KindDuration, true
	}
	return 0, false
}

// Value is a temporal value. Its String method is the canonical Cypher
// rendering, which Parse accepts back.
type Value interface {
	Kind() Kind
	String() string
}

// Date is a calendar date.
type Date struct{ t time.Time } // midnight UTC

// LocalTime is a time of day without a zone.
type LocalTime struct{ ns int64 } // nanoseconds since midnight

// Time is a time of day with a UTC offset.
type Time struct {
	ns  int64 // local nanoseconds since midnight
	off int   // offset from UTC in seconds
}

// LocalDateTime is a date and time without a zone.
type LocalDateTime struct{ t time.Time } // wall clock, labelled UTC

// DateTime is a date and time with a UTC offset and optionally a region zone.
type DateTime struct {
	t     time.Time // carries the zone location
	named bool      // true when the zone is a region ("Europe/Stockholm")
}

// Duration is an amount of time in months, days and seconds, kept separately
// because months and days have no fixed length.
type Duration struct {
	Months  int64
	Days    int64
	Seconds int64
	Nanos   int64 // 0 <= Nanos < 1e9; the sign of the whole is carried by Seconds
}

func (Date) Kind() Kind          { return KindDate }
func (LocalTime) Kind() Kind     { return KindLocalTime }
func (Time) Kind() Kind          { return KindTime }
func (LocalDateTime) Kind() Kind { return KindLocalDateTime }
func (DateTime) Kind() Kind      { return KindDateTime }
func (Duration) Kind() Kind      { return KindDuration }

const (
	nsPerSec = int64(1e9)
	nsPerDay = 86400 * nsPerSec
)

// ─── constructors from components ───────────────────────────────────────────

func daysIn(y, m int) int {
	switch m {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}

func newDate(y, m, d int) (Date, error) {
	if y < -999999999 || y > 999999999 {
		return Date{}, fmt.Errorf("year %d is out of range", y)
	}
	if m < 1 || m > 12 {
		return Date{}, fmt.Errorf("month %d is out of range", m)
	}
	if d < 1 || d > daysIn(y, m) {
		return Date{}, fmt.Errorf("day %d is out of range for %d-%02d", d, y, m)
	}
	return Date{time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)}, nil
}

func newLocalTime(h, mi, s int, nanos int64) (LocalTime, error) {
	if h < 0 || h > 23 {
		return LocalTime{}, fmt.Errorf("hour %d is out of range", h)
	}
	if mi < 0 || mi > 59 {
		return LocalTime{}, fmt.Errorf("minute %d is out of range", mi)
	}
	if s < 0 || s > 59 {
		return LocalTime{}, fmt.Errorf("second %d is out of range", s)
	}
	if nanos < 0 || nanos >= nsPerSec {
		return LocalTime{}, fmt.Errorf("nanosecond %d is out of range", nanos)
	}
	return LocalTime{int64(h)*3600*nsPerSec + int64(mi)*60*nsPerSec + int64(s)*nsPerSec + nanos}, nil
}

// ─── components ─────────────────────────────────────────────────────────────

func (d Date) ymd() (int, int, int) {
	y, m, dd := d.t.Date()
	return y, int(m), dd
}

func (t LocalTime) hms() (h, m, s int, nanos int64) {
	secs := t.ns / nsPerSec
	return int(secs / 3600), int(secs % 3600 / 60), int(secs % 60), t.ns % nsPerSec
}

// ─── rendering ──────────────────────────────────────────────────────────────

func fmtYear(y int) string {
	switch {
	case y < 0:
		return fmt.Sprintf("-%04d", -y)
	case y > 9999:
		return fmt.Sprintf("+%d", y)
	}
	return fmt.Sprintf("%04d", y)
}

func (d Date) String() string {
	y, m, dd := d.ymd()
	return fmt.Sprintf("%s-%02d-%02d", fmtYear(y), m, dd)
}

// fraction renders nanoseconds as 3, 6 or 9 digits, dropping trailing groups
// of zeros.
func fraction(nanos int64) string {
	s := fmt.Sprintf("%09d", nanos)
	switch {
	case strings.HasSuffix(s, "000000"):
		return s[:3]
	case strings.HasSuffix(s, "000"):
		return s[:6]
	}
	return s
}

func (t LocalTime) String() string {
	h, m, s, nanos := t.hms()
	out := fmt.Sprintf("%02d:%02d", h, m)
	if s != 0 || nanos != 0 {
		out += fmt.Sprintf(":%02d", s)
	}
	if nanos != 0 {
		out += "." + fraction(nanos)
	}
	return out
}

func fmtOffset(off int) string {
	if off == 0 {
		return "Z"
	}
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	out := fmt.Sprintf("%s%02d:%02d", sign, off/3600, off%3600/60)
	if off%60 != 0 {
		out += fmt.Sprintf(":%02d", off%60)
	}
	return out
}

func (t Time) String() string { return LocalTime{t.ns}.String() + fmtOffset(t.off) }

func (t LocalDateTime) String() string {
	return Date{midnight(t.t)}.String() + "T" + LocalTime{wallNanos(t.t)}.String()
}

func (t DateTime) String() string {
	_, off := t.t.Zone()
	out := Date{midnight(t.t)}.String() + "T" + LocalTime{wallNanos(t.t)}.String() + fmtOffset(off)
	if t.named {
		out += "[" + t.t.Location().String() + "]"
	}
	return out
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func wallNanos(t time.Time) int64 {
	return int64(t.Hour())*3600*nsPerSec + int64(t.Minute())*60*nsPerSec + int64(t.Second())*nsPerSec + int64(t.Nanosecond())
}

// String renders the duration in ISO 8601 form, e.g. P1Y2M3DT4H5M6.5S. The
// sign of each component is kept, so mixed signs render as P1M-2DT3H.
func (d Duration) String() string {
	var sb strings.Builder
	sb.WriteString("P")
	if d.Months != 0 {
		if y := d.Months / 12; y != 0 {
			sb.WriteString(strconv.FormatInt(y, 10) + "Y")
		}
		if m := d.Months % 12; m != 0 {
			sb.WriteString(strconv.FormatInt(m, 10) + "M")
		}
	}
	if d.Days != 0 {
		sb.WriteString(strconv.FormatInt(d.Days, 10) + "D")
	}
	if d.Seconds != 0 || d.Nanos != 0 || (d.Months == 0 && d.Days == 0) {
		sb.WriteString("T")
		secs, nanos := d.Seconds, d.Nanos
		if secs < 0 && nanos > 0 { // express as a negative total
			secs++
			nanos -= nsPerSec
		}
		if h := secs / 3600; h != 0 {
			sb.WriteString(strconv.FormatInt(h, 10) + "H")
		}
		if m := secs % 3600 / 60; m != 0 {
			sb.WriteString(strconv.FormatInt(m, 10) + "M")
		}
		s := secs % 60
		if s != 0 || nanos != 0 || (secs == 0 && sb.String() == "PT") {
			neg := s < 0 || nanos < 0
			if nanos < 0 {
				nanos = -nanos
			}
			if s < 0 {
				s = -s
			}
			if neg {
				sb.WriteString("-")
			}
			sb.WriteString(strconv.FormatInt(s, 10))
			if nanos != 0 {
				sb.WriteString("." + strings.TrimRight(fmt.Sprintf("%09d", nanos), "0"))
			}
			sb.WriteString("S")
		}
	}
	return sb.String()
}
