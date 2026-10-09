package temporal

import (
	"fmt"
	"strings"
	"time"
)

// Truncate implements date.truncate, localtime.truncate, time.truncate,
// localdatetime.truncate and datetime.truncate: the value is cut down to the
// start of unit and the entries of m are then applied on top (so m may carry
// components such as {day: 2} or {timezone: 'Europe/Stockholm'}).
func Truncate(kind Kind, unit string, v Value, m map[string]any) (Value, error) {
	if v.Kind() == KindDuration {
		return nil, fmt.Errorf("%s.truncate needs a temporal value", kind.Name())
	}
	p := partsOf(v)
	u := strings.ToLower(unit)
	dateUnit := map[string]bool{"millennium": true, "century": true, "decade": true, "year": true,
		"weekyear": true, "quarter": true, "month": true, "week": true, "day": true}
	timeUnit := map[string]bool{"hour": true, "minute": true, "second": true, "millisecond": true, "microsecond": true}
	if !dateUnit[u] && !timeUnit[u] {
		return nil, fmt.Errorf("unknown truncation unit %q", unit)
	}
	wantsDate := kind == KindDate || kind == KindLocalDateTime || kind == KindDateTime
	wantsTime := kind != KindDate
	if kind == KindDate && timeUnit[u] {
		return nil, fmt.Errorf("cannot truncate a date to %s", unit)
	}
	if wantsDate && p.date == nil {
		return nil, fmt.Errorf("cannot truncate %s to a %s", v.Kind().Name(), kind.Name())
	}
	if kind != KindDate && p.time == nil && !(wantsDate && p.date != nil) {
		return nil, fmt.Errorf("cannot truncate %s to a %s", v.Kind().Name(), kind.Name())
	}

	var date Date
	if p.date != nil {
		date = *p.date
		if dateUnit[u] {
			d, err := truncateDate(date, u)
			if err != nil {
				return nil, err
			}
			date = d
		}
	}
	var clock LocalTime
	if wantsTime {
		if p.time != nil {
			clock = *p.time
		}
		switch {
		case dateUnit[u]:
			clock = LocalTime{}
		default:
			clock = truncateClock(clock, u)
		}
	}

	// The zone stays with the local fields; a timezone entry replaces it.
	args := map[string]any{}
	for k, val := range m {
		args[k] = val
	}
	if _, ok := args["timezone"]; !ok && p.zone != nil && (kind == KindTime || kind == KindDateTime) {
		args["timezone"] = zoneString(*p.zone)
	}
	switch kind {
	case KindDate:
		args["date"] = date
	case KindLocalTime:
		args["time"] = clock
	case KindTime:
		args["time"] = clock
	case KindLocalDateTime:
		args["datetime"] = combineLocal(date, clock)
	case KindDateTime:
		args["datetime"] = combineLocal(date, clock)
	}
	return fromMap(kind, args)
}

func zoneString(z zoneSpec) string {
	if z.named {
		return z.loc.String()
	}
	_, off := time.Date(2000, 1, 1, 0, 0, 0, 0, z.loc).Zone()
	return fmtOffset(off)
}

func truncateDate(d Date, unit string) (Date, error) {
	y := d.Year()
	switch unit {
	case "millennium":
		return newDate(int(floorDiv(int64(y), 1000)*1000), 1, 1)
	case "century":
		return newDate(int(floorDiv(int64(y), 100)*100), 1, 1)
	case "decade":
		return newDate(int(floorDiv(int64(y), 10)*10), 1, 1)
	case "year":
		return newDate(y, 1, 1)
	case "weekyear":
		return Date{isoWeek1Monday(d.WeekYear())}, nil
	case "quarter":
		return newDate(y, (d.Quarter()-1)*3+1, 1)
	case "month":
		return newDate(y, d.Month(), 1)
	case "week":
		return Date{d.t.AddDate(0, 0, -(d.WeekDay() - 1))}, nil
	}
	return d, nil
}

func truncateClock(t LocalTime, unit string) LocalTime {
	switch unit {
	case "hour":
		return LocalTime{t.ns / (3600 * nsPerSec) * (3600 * nsPerSec)}
	case "minute":
		return LocalTime{t.ns / (60 * nsPerSec) * (60 * nsPerSec)}
	case "second":
		return LocalTime{t.ns / nsPerSec * nsPerSec}
	case "millisecond":
		return LocalTime{t.ns / 1000000 * 1000000}
	case "microsecond":
		return LocalTime{t.ns / 1000 * 1000}
	}
	return t
}
