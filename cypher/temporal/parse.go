package temporal

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reCalendar = regexp.MustCompile(`^([+-]?\d{4,9})-(\d{2})-(\d{2})$`)
	reYearMon  = regexp.MustCompile(`^([+-]?\d{4,9})-(\d{2})$`)
	reOrdinal  = regexp.MustCompile(`^([+-]?\d{4,9})-(\d{3})$`)
	reWeek     = regexp.MustCompile(`^([+-]?\d{4,9})-?W(\d{2})(?:-?(\d))?$`)
	reCompact  = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})$`)
	reCompMon  = regexp.MustCompile(`^(\d{4})(\d{2})$`)
	reCompOrd  = regexp.MustCompile(`^(\d{4})(\d{3})$`)
	reYear     = regexp.MustCompile(`^([+-]?\d{4,9})$`)
	reTime     = regexp.MustCompile(`^(\d{2})(?::?(\d{2})(?::?(\d{2})(?:[.,](\d{1,9}))?)?)?$`)
)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// parseDate parses every ISO 8601 date form Cypher accepts.
func parseDate(s string) (Date, error) {
	bad := func() (Date, error) { return Date{}, fmt.Errorf("cannot parse %q as a date", s) }
	if m := reWeek.FindStringSubmatch(s); m != nil {
		dow := 1
		if m[3] != "" {
			dow = atoi(m[3])
		}
		return dateFromWeek(atoi(m[1]), atoi(m[2]), dow)
	}
	switch {
	case reCalendar.MatchString(s):
		m := reCalendar.FindStringSubmatch(s)
		return newDate(atoi(m[1]), atoi(m[2]), atoi(m[3]))
	case reYearMon.MatchString(s):
		m := reYearMon.FindStringSubmatch(s)
		return newDate(atoi(m[1]), atoi(m[2]), 1)
	case reOrdinal.MatchString(s):
		m := reOrdinal.FindStringSubmatch(s)
		return dateFromOrdinal(atoi(m[1]), atoi(m[2]))
	case reCompact.MatchString(s):
		m := reCompact.FindStringSubmatch(s)
		return newDate(atoi(m[1]), atoi(m[2]), atoi(m[3]))
	case reCompMon.MatchString(s):
		m := reCompMon.FindStringSubmatch(s)
		return newDate(atoi(m[1]), atoi(m[2]), 1)
	case reCompOrd.MatchString(s):
		m := reCompOrd.FindStringSubmatch(s)
		return dateFromOrdinal(atoi(m[1]), atoi(m[2]))
	case reYear.MatchString(s):
		return newDate(atoi(s), 1, 1)
	}
	return bad()
}

// splitZone separates a trailing UTC offset (Z, +hh:mm, -hhmm, …) from a time.
func splitZone(s string) (clock string, off int, hasOff bool, err error) {
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		return s[:len(s)-1], 0, true, nil
	}
	if i := strings.LastIndexAny(s, "+-"); i > 0 {
		o, ok := parseOffset(s[i:])
		if !ok {
			return "", 0, false, fmt.Errorf("cannot parse %q as a time zone offset", s[i:])
		}
		return s[:i], o, true, nil
	}
	return s, 0, false, nil
}

func parseClock(s string) (LocalTime, error) {
	m := reTime.FindStringSubmatch(s)
	if m == nil {
		return LocalTime{}, fmt.Errorf("cannot parse %q as a time", s)
	}
	var nanos int64
	if m[4] != "" {
		n, _ := strconv.ParseInt((m[4] + "000000000")[:9], 10, 64)
		nanos = n
	}
	return newLocalTime(atoi(m[1]), atoi(m[2]), atoi(m[3]), nanos)
}

func parseLocalTime(s string) (LocalTime, error) {
	clock, _, hasOff, err := splitZone(s)
	if err != nil || hasOff {
		return LocalTime{}, fmt.Errorf("cannot parse %q as a local time", s)
	}
	return parseClock(clock)
}

func parseTime(s string) (Time, error) {
	clock, off, _, err := splitZone(s)
	if err != nil {
		return Time{}, err
	}
	t, err := parseClock(clock)
	if err != nil {
		return Time{}, err
	}
	return Time{ns: t.ns, off: off}, nil
}

// splitDateTime splits "date T time" and strips a trailing "[Region]".
func splitDateTime(s string) (date, rest, region string, err error) {
	if i := strings.IndexByte(s, '['); i >= 0 {
		if !strings.HasSuffix(s, "]") {
			return "", "", "", fmt.Errorf("cannot parse %q as a datetime", s)
		}
		region = s[i+1 : len(s)-1]
		s = s[:i]
	}
	i := strings.IndexAny(s, "Tt")
	if i < 0 {
		return s, "", region, nil
	}
	return s[:i], s[i+1:], region, nil
}

func parseLocalDateTime(s string) (LocalDateTime, error) {
	ds, ts, region, err := splitDateTime(s)
	if err != nil || region != "" {
		return LocalDateTime{}, fmt.Errorf("cannot parse %q as a local datetime", s)
	}
	d, err := parseDate(ds)
	if err != nil {
		return LocalDateTime{}, err
	}
	var t LocalTime
	if ts != "" {
		if t, err = parseLocalTime(ts); err != nil {
			return LocalDateTime{}, err
		}
	}
	return combineLocal(d, t), nil
}

func parseDateTime(s string) (DateTime, error) {
	ds, ts, region, err := splitDateTime(s)
	if err != nil {
		return DateTime{}, err
	}
	d, err := parseDate(ds)
	if err != nil {
		return DateTime{}, err
	}
	var t LocalTime
	zone := utc
	hasOff := false
	if ts != "" {
		clock, off, ho, err := splitZone(ts)
		if err != nil {
			return DateTime{}, err
		}
		if t, err = parseClock(clock); err != nil {
			return DateTime{}, err
		}
		if ho {
			zone, hasOff = fixedZone(off), true
		}
	}
	if region != "" {
		z, err := parseZone(region)
		if err != nil {
			return DateTime{}, err
		}
		zone = z
	}
	_ = hasOff
	return zone.at(combineLocal(d, t).t), nil
}

func combineLocal(d Date, t LocalTime) LocalDateTime {
	return LocalDateTime{d.t.Add(time.Duration(t.ns))}
}

// ─── durations ──────────────────────────────────────────────────────────────

var reDurNum = regexp.MustCompile(`^([+-]?\d+(?:[.,]\d+)?)([YMWDHS])`)

func parseRat(s string) *big.Rat {
	r, _ := new(big.Rat).SetString(strings.Replace(s, ",", ".", 1))
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// parseDuration parses ISO 8601 durations (P1Y2M3W4DT5H6M7.8S) and the
// alternative calendar form P2012-02-02T14:37:21.545.
func parseDuration(s string) (Duration, error) {
	bad := fmt.Errorf("cannot parse %q as a duration", s)
	orig := s
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if len(s) < 2 || (s[0] != 'P' && s[0] != 'p') {
		return Duration{}, bad
	}
	s = s[1:]
	p := newDurParts()
	if m := regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?$`).FindStringSubmatch(s); m != nil {
		p.years.SetInt64(int64(atoi(m[1])))
		p.months.SetInt64(int64(atoi(m[2])))
		p.days.SetInt64(int64(atoi(m[3])))
		p.hours.SetInt64(int64(atoi(m[4])))
		p.minutes.SetInt64(int64(atoi(m[5])))
		p.seconds.SetInt64(int64(atoi(m[6])))
		if m[7] != "" {
			n, _ := strconv.ParseInt((m[7] + "000000000")[:9], 10, 64)
			p.nanos.SetInt64(n)
		}
		d, err := p.build()
		return negateIf(d, err, neg)
	}
	inTime := false
	if s == "" || s == "T" {
		return Duration{}, bad
	}
	for s != "" {
		if s[0] == 'T' || s[0] == 't' {
			if inTime {
				return Duration{}, bad
			}
			inTime, s = true, s[1:]
			continue
		}
		m := reDurNum.FindStringSubmatch(strings.ToUpper(s))
		if m == nil {
			return Duration{}, fmt.Errorf("cannot parse %q as a duration", orig)
		}
		v := parseRat(m[1])
		switch {
		case m[2] == "Y" && !inTime:
			p.years = v
		case m[2] == "M" && !inTime:
			p.months = v
		case m[2] == "W" && !inTime:
			p.weeks = v
		case m[2] == "D" && !inTime:
			p.days = v
		case m[2] == "H" && inTime:
			p.hours = v
		case m[2] == "M" && inTime:
			p.minutes = v
		case m[2] == "S" && inTime:
			p.seconds = v
		default:
			return Duration{}, bad
		}
		s = s[len(m[0]):]
	}
	d, err := p.build()
	return negateIf(d, err, neg)
}

func negateIf(d Duration, err error, neg bool) (Duration, error) {
	if err != nil || !neg {
		return d, err
	}
	return d.Neg(), nil
}

// Parse parses the canonical string form of a value of the given kind.
func Parse(kind Kind, s string) (Value, error) {
	switch kind {
	case KindDate:
		return parseDate(s)
	case KindLocalTime:
		return parseLocalTime(s)
	case KindTime:
		return parseTime(s)
	case KindLocalDateTime:
		return parseLocalDateTime(s)
	case KindDateTime:
		return parseDateTime(s)
	case KindDuration:
		return parseDuration(s)
	}
	return nil, fmt.Errorf("unknown temporal kind")
}
