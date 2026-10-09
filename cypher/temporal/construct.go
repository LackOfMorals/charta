package temporal

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Now returns the value of the given kind at the instant at, in the zone named
// by m's "timezone" entry (UTC when absent).
func Now(kind Kind, m map[string]any, at time.Time) (Value, error) {
	zone := utc
	if tz, ok := m["timezone"]; ok {
		s, ok := tz.(string)
		if !ok {
			return nil, fmt.Errorf("timezone must be a string")
		}
		z, err := parseZone(s)
		if err != nil {
			return nil, err
		}
		zone = z
	}
	now := at.In(zone.loc)
	dt := DateTime{t: now, named: zone.named}
	return convert(kind, dt)
}

// Construct builds a value of the given kind from a string, a map or another
// temporal value. A nil argument is the caller's responsibility (null in, null
// out).
func Construct(kind Kind, arg any) (Value, error) {
	switch a := arg.(type) {
	case string:
		return Parse(kind, a)
	case map[string]any:
		if kind == KindDuration {
			return durationFromMap(a)
		}
		return fromMap(kind, a)
	case Value:
		if kind == KindDuration {
			if d, ok := a.(Duration); ok {
				return d, nil
			}
			return nil, fmt.Errorf("cannot convert %s to a duration", a.Kind().Name())
		}
		return convert(kind, a)
	}
	return nil, fmt.Errorf("%s() expects a string, a map or a temporal value", kind.Name())
}

// ─── conversions between temporal kinds ─────────────────────────────────────

// parts is a temporal value split into the optional pieces maps can combine.
type parts struct {
	date    *Date
	time    *LocalTime // local wall-clock time
	zone    *zoneSpec  // zone of a zoned time or datetime
	instant *time.Time // set for zoned values: the instant, to convert zones
}

func partsOf(v Value) parts {
	var p parts
	switch x := v.(type) {
	case Date:
		p.date = &x
	case LocalTime:
		p.time = &x
	case Time:
		lt := LocalTime{x.ns}
		z := fixedZone(x.off)
		p.time, p.zone = &lt, &z
	case LocalDateTime:
		d, lt := Date{midnight(x.t)}, LocalTime{wallNanos(x.t)}
		p.date, p.time = &d, &lt
	case DateTime:
		d, lt := Date{midnight(x.t)}, LocalTime{wallNanos(x.t)}
		z := x.zone()
		it := x.t
		p.date, p.time, p.zone, p.instant = &d, &lt, &z, &it
	}
	return p
}

// convert implements date(x), localtime(x), … for a single temporal argument.
func convert(kind Kind, v Value) (Value, error) {
	if v.Kind() == kind {
		return v, nil
	}
	p := partsOf(v)
	switch kind {
	case KindDate:
		if p.date == nil {
			return nil, fmt.Errorf("cannot convert %s to a date", v.Kind().Name())
		}
		return *p.date, nil
	case KindLocalTime:
		if p.time == nil {
			return nil, fmt.Errorf("cannot convert %s to a local time", v.Kind().Name())
		}
		return *p.time, nil
	case KindTime:
		if p.time == nil {
			return nil, fmt.Errorf("cannot convert %s to a time", v.Kind().Name())
		}
		if p.zone == nil {
			return Time{ns: p.time.ns}, nil
		}
		if p.date != nil {
			return Time{ns: p.time.ns, off: p.zone.offsetAt(combineLocal(*p.date, *p.time).t)}, nil
		}
		off, _ := p.instantOffset()
		return Time{ns: p.time.ns, off: off}, nil
	case KindLocalDateTime:
		if p.date == nil {
			return nil, fmt.Errorf("cannot convert %s to a local datetime", v.Kind().Name())
		}
		t := LocalTime{}
		if p.time != nil {
			t = *p.time
		}
		return combineLocal(*p.date, t), nil
	case KindDateTime:
		if p.date == nil {
			return nil, fmt.Errorf("cannot convert %s to a datetime", v.Kind().Name())
		}
		t := LocalTime{}
		if p.time != nil {
			t = *p.time
		}
		if dt, ok := v.(DateTime); ok {
			return dt, nil
		}
		return utc.at(combineLocal(*p.date, t).t), nil
	}
	return nil, fmt.Errorf("cannot convert to %s", kind.Name())
}

func (p parts) instantOffset() (int, bool) {
	if p.instant != nil {
		_, off := p.instant.Zone()
		return off, true
	}
	return 0, false
}

// ─── maps ───────────────────────────────────────────────────────────────────

var (
	dateKeys = []string{"year", "month", "day", "week", "dayOfWeek", "ordinalDay", "quarter", "dayOfQuarter"}
	timeKeys = []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}
)

func intArg(m map[string]any, key string) (int, bool, error) {
	v, ok := m[key]
	if !ok {
		return 0, false, nil
	}
	switch x := v.(type) {
	case int64:
		if x > math.MaxInt32*2 || x < math.MinInt32*2 {
			return 0, false, fmt.Errorf("%s %d is out of range", key, x)
		}
		return int(x), true, nil
	case float64:
		if x == math.Trunc(x) {
			return int(x), true, nil
		}
	}
	return 0, false, fmt.Errorf("%s must be an integer", key)
}

// fromMap builds date, localtime, time, localdatetime or datetime from a map.
func fromMap(kind Kind, m map[string]any) (Value, error) {
	allowed := map[string]bool{"timezone": true, "date": true, "time": true, "datetime": true}
	for _, k := range dateKeys {
		allowed[k] = true
	}
	for _, k := range timeKeys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			names := make([]string, 0, len(allowed))
			for n := range allowed {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("unknown key %q for %s() (allowed: %s)", k, kind.Name(), strings.Join(names, ", "))
		}
	}
	hasAny := func(keys []string) bool {
		for _, k := range keys {
			if _, ok := m[k]; ok {
				return true
			}
		}
		return false
	}
	// Nothing but a timezone means "now".
	if len(m) == 1 {
		if _, ok := m["timezone"]; ok {
			return Now(kind, m, time.Now())
		}
	}

	// Base values.
	var base parts
	for _, key := range []string{"datetime", "date", "time"} {
		raw, ok := m[key]
		if !ok {
			continue
		}
		tv, ok := raw.(Value)
		if !ok || tv.Kind() == KindDuration {
			return nil, fmt.Errorf("%s must be a temporal value", key)
		}
		p := partsOf(tv)
		switch key {
		case "date":
			if p.date == nil {
				return nil, fmt.Errorf("date must be a value with a date part")
			}
			base.date = p.date
		case "time":
			if p.time == nil {
				return nil, fmt.Errorf("time must be a value with a time part")
			}
			base.time, base.zone, base.instant = p.time, p.zone, p.instant
		case "datetime":
			if p.date == nil || p.time == nil {
				return nil, fmt.Errorf("datetime must be a datetime value")
			}
			base = p
		}
	}

	wantsDate := kind == KindDate || kind == KindLocalDateTime || kind == KindDateTime
	wantsTime := kind == KindLocalTime || kind == KindTime || kind == KindLocalDateTime || kind == KindDateTime
	wantsZone := kind == KindTime || kind == KindDateTime

	// The requested zone, if any.
	var tz *zoneSpec
	if raw, ok := m["timezone"]; ok && wantsZone {
		str, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("timezone must be a string")
		}
		z, err := parseZone(str)
		if err != nil {
			return nil, err
		}
		tz = &z
	}
	// A zoned datetime base re-expressed in another zone moves date and time.
	if tz != nil && base.instant != nil && m["datetime"] != nil {
		conv := base.instant.In(tz.loc)
		d, lt := Date{midnight(conv)}, LocalTime{wallNanos(conv)}
		base.date, base.time = &d, &lt
		base.zone = tz
	}

	var date Date
	if wantsDate {
		d, err := resolveDate(m, base.date, hasAny)
		if err != nil {
			return nil, err
		}
		date = d
	}
	// A zoned time base re-expressed in another zone keeps its instant on the
	// date being built (so region rules apply on that date).
	if tz != nil && base.zone != nil && base.time != nil && base.zone != tz {
		on := Date{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		switch {
		case wantsDate:
			on = date
		case base.date != nil:
			on = *base.date
		}
		src := base.zone.at(combineLocal(on, *base.time).t)
		conv := src.t.In(tz.loc)
		lt := LocalTime{wallNanos(conv)}
		base.time = &lt
	}
	var tm LocalTime
	if wantsTime {
		t, err := resolveTime(m, base.time)
		if err != nil {
			return nil, err
		}
		tm = t
	}

	zone := utc
	switch {
	case tz != nil:
		zone = *tz
	case base.zone != nil && wantsZone:
		zone = *base.zone
	}

	switch kind {
	case KindDate:
		return date, nil
	case KindLocalTime:
		return tm, nil
	case KindTime:
		return Time{ns: tm.ns, off: zone.offsetAt(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(tm.ns)))}, nil
	case KindLocalDateTime:
		return combineLocal(date, tm), nil
	default:
		return zone.at(combineLocal(date, tm).t), nil
	}
}

func mod(a, b int64) int64 {
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}

// resolveDate combines the date keys of m with an optional base date.
func resolveDate(m map[string]any, base *Date, hasAny func([]string) bool) (Date, error) {
	get := func(key string, def int) (int, error) {
		v, ok, err := intArg(m, key)
		if err != nil {
			return 0, err
		}
		if !ok {
			return def, nil
		}
		return v, nil
	}
	weekForm := hasAny([]string{"week", "dayOfWeek"})
	ordinalForm := hasAny([]string{"ordinalDay"})
	quarterForm := hasAny([]string{"quarter", "dayOfQuarter"})
	if _, hasYear := m["year"]; !hasYear && base == nil {
		return Date{}, fmt.Errorf("a year is required to construct a date")
	}
	var by, bm, bd, bwy, bw, bdow, bo, bq, bdq int
	by, bm, bd, bwy, bw, bdow, bo, bq, bdq = 0, 1, 1, 0, 1, 1, 1, 1, 1
	if base != nil {
		by, bm, bd = base.Year(), base.Month(), base.Day()
		bwy, bw, bdow = base.WeekYear(), base.Week(), base.WeekDay()
		bo, bq, bdq = base.Ordinal(), base.Quarter(), base.DayOfQuarter()
	}
	switch {
	case weekForm:
		def := by
		if base != nil {
			def = bwy
		}
		y, err := get("year", def)
		if err != nil {
			return Date{}, err
		}
		w, err := get("week", bw)
		if err != nil {
			return Date{}, err
		}
		dow, err := get("dayOfWeek", bdow)
		if err != nil {
			return Date{}, err
		}
		return dateFromWeek(y, w, dow)
	case ordinalForm:
		y, err := get("year", by)
		if err != nil {
			return Date{}, err
		}
		o, err := get("ordinalDay", bo)
		if err != nil {
			return Date{}, err
		}
		return dateFromOrdinal(y, o)
	case quarterForm:
		y, err := get("year", by)
		if err != nil {
			return Date{}, err
		}
		q, err := get("quarter", bq)
		if err != nil {
			return Date{}, err
		}
		dq, err := get("dayOfQuarter", bdq)
		if err != nil {
			return Date{}, err
		}
		return dateFromQuarter(y, q, dq)
	}
	y, err := get("year", by)
	if err != nil {
		return Date{}, err
	}
	mo, err := get("month", bm)
	if err != nil {
		return Date{}, err
	}
	d, err := get("day", bd)
	if err != nil {
		return Date{}, err
	}
	return newDate(y, mo, d)
}

// resolveTime combines the time keys of m with an optional base time.
func resolveTime(m map[string]any, base *LocalTime) (LocalTime, error) {
	var h, mi, s int
	var ms, us, ns int64
	if base != nil {
		bh, bmi, bs, bn := base.hms()
		h, mi, s = bh, bmi, bs
		ms, us, ns = bn/1000000, bn/1000%1000, bn%1000
	}
	set := func(key string, dst *int) error {
		v, ok, err := intArg(m, key)
		if err != nil {
			return err
		}
		if ok {
			*dst = v
		}
		return nil
	}
	for key, dst := range map[string]*int{"hour": &h, "minute": &mi, "second": &s} {
		if err := set(key, dst); err != nil {
			return LocalTime{}, err
		}
	}
	for key, dst := range map[string]*int64{"millisecond": &ms, "microsecond": &us, "nanosecond": &ns} {
		v, ok, err := intArg(m, key)
		if err != nil {
			return LocalTime{}, err
		}
		if ok {
			*dst = int64(v)
		}
	}
	return newLocalTime(h, mi, s, ms*1000000+us*1000+ns)
}

// durationFromMap builds a duration from a map of (possibly fractional)
// amounts.
func durationFromMap(m map[string]any) (Duration, error) {
	p := newDurParts()
	slots := map[string]**big.Rat{
		"years": &p.years, "months": &p.months, "weeks": &p.weeks, "days": &p.days,
		"hours": &p.hours, "minutes": &p.minutes, "seconds": &p.seconds,
		"milliseconds": &p.millis, "microseconds": &p.micros, "nanoseconds": &p.nanos,
	}
	for k, v := range m {
		slot, ok := slots[k]
		if !ok {
			return Duration{}, fmt.Errorf("unknown key %q for duration()", k)
		}
		switch x := v.(type) {
		case int64:
			*slot = ratInt(x)
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return Duration{}, fmt.Errorf("%s must be a finite number", k)
			}
			*slot = ratFloat(x)
		default:
			return Duration{}, fmt.Errorf("%s must be a number", k)
		}
	}
	return p.build()
}

// FromEpoch builds a UTC datetime from seconds and nanoseconds since the Unix
// epoch.
func FromEpoch(seconds, nanos int64) DateTime {
	return DateTime{t: time.Unix(seconds, nanos).UTC()}
}

// FromEpochMillis builds a UTC datetime from milliseconds since the Unix epoch.
func FromEpochMillis(ms int64) DateTime {
	return DateTime{t: time.UnixMilli(ms).UTC()}
}
