package temporal

// Property returns a component of a temporal value, e.g. d.year or t.offset.
// ok is false when the value has no such component.
func Property(v Value, name string) (any, bool) {
	switch x := v.(type) {
	case Date:
		return dateProp(x, name)
	case LocalTime:
		return timeProp(x.ns, name)
	case Time:
		if r, ok := timeProp(x.ns, name); ok {
			return r, true
		}
		return offsetProp(x.off, fmtOffset2(x.off), name)
	case LocalDateTime:
		if r, ok := dateProp(Date{midnight(x.t)}, name); ok {
			return r, true
		}
		return timeProp(wallNanos(x.t), name)
	case DateTime:
		if r, ok := dateProp(Date{midnight(x.t)}, name); ok {
			return r, true
		}
		if r, ok := timeProp(wallNanos(x.t), name); ok {
			return r, true
		}
		switch name {
		case "epochSeconds":
			return x.t.Unix(), true
		case "epochMillis":
			return x.t.UnixMilli(), true
		case "timezone":
			return x.ZoneName(), true
		}
		return offsetProp(x.Offset(), fmtOffset2(x.Offset()), name)
	case Duration:
		return durationProp(x, name)
	}
	return nil, false
}

// fmtOffset2 renders an offset as +hh:mm (never Z), as the "offset" accessor does.
func fmtOffset2(off int) string {
	if off == 0 {
		return "Z"
	}
	return fmtOffset(off)
}

func offsetProp(off int, rendered, name string) (any, bool) {
	switch name {
	case "timezone", "offset":
		return rendered, true
	case "offsetMinutes":
		return int64(off / 60), true
	case "offsetSeconds":
		return int64(off), true
	}
	return nil, false
}

func dateProp(d Date, name string) (any, bool) {
	switch name {
	case "year":
		return int64(d.Year()), true
	case "quarter":
		return int64(d.Quarter()), true
	case "month":
		return int64(d.Month()), true
	case "week":
		return int64(d.Week()), true
	case "weekYear":
		return int64(d.WeekYear()), true
	case "day":
		return int64(d.Day()), true
	case "ordinalDay":
		return int64(d.Ordinal()), true
	case "weekDay":
		return int64(d.WeekDay()), true
	case "dayOfQuarter":
		return int64(d.DayOfQuarter()), true
	}
	return nil, false
}

func timeProp(ns int64, name string) (any, bool) {
	h, m, s, nanos := LocalTime{ns}.hms()
	switch name {
	case "hour":
		return int64(h), true
	case "minute":
		return int64(m), true
	case "second":
		return int64(s), true
	case "millisecond":
		return nanos / 1000000, true
	case "microsecond":
		return nanos / 1000, true
	case "nanosecond":
		return nanos, true
	}
	return nil, false
}

func durationProp(d Duration, name string) (any, bool) {
	// Totals are signed from the component values; the time part is measured
	// from the effective (sign-carrying) number of seconds.
	secs := d.Seconds
	if secs < 0 && d.Nanos > 0 {
		secs++
	}
	switch name {
	case "years":
		return d.Months / 12, true
	case "quarters":
		return d.Months / 3, true
	case "months":
		return d.Months, true
	case "weeks":
		return d.Days / 7, true
	case "days":
		return d.Days, true
	case "hours":
		return secs / 3600, true
	case "minutes":
		return secs / 60, true
	case "seconds":
		return d.Seconds, true
	case "milliseconds":
		return d.Seconds*1000 + d.Nanos/1000000, true
	case "microseconds":
		return d.Seconds*1000000 + d.Nanos/1000, true
	case "nanoseconds":
		return d.Seconds*nsPerSec + d.Nanos, true
	case "quartersOfYear":
		return d.Months % 12 / 3, true
	case "monthsOfQuarter":
		return d.Months % 3, true
	case "monthsOfYear":
		return d.Months % 12, true
	case "daysOfWeek":
		return d.Days % 7, true
	case "minutesOfHour":
		return secs % 3600 / 60, true
	case "secondsOfMinute":
		return secs % 60, true
	case "millisecondsOfSecond":
		return d.Nanos / 1000000, true
	case "microsecondsOfSecond":
		return d.Nanos / 1000, true
	case "nanosecondsOfSecond":
		return d.Nanos, true
	}
	return nil, false
}
