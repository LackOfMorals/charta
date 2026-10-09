package temporal

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// zoneSpec is a parsed time zone: a fixed offset or a named region.
type zoneSpec struct {
	loc   *time.Location
	named bool
}

var utc = zoneSpec{loc: time.UTC}

func fixedZone(off int) zoneSpec {
	if off == 0 {
		return utc
	}
	return zoneSpec{loc: time.FixedZone("", off)}
}

// parseOffset parses Z, ±HH, ±HHMM, ±HH:MM, ±HHMMSS or ±HH:MM:SS into seconds.
func parseOffset(s string) (int, bool) {
	if s == "Z" || s == "z" {
		return 0, true
	}
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	sign := 1
	if s[0] == '-' {
		sign = -1
	}
	digits := strings.ReplaceAll(s[1:], ":", "")
	if len(digits) != 2 && len(digits) != 4 && len(digits) != 6 {
		return 0, false
	}
	if _, err := strconv.Atoi(digits); err != nil {
		return 0, false
	}
	h, _ := strconv.Atoi(digits[:2])
	m, sec := 0, 0
	if len(digits) >= 4 {
		m, _ = strconv.Atoi(digits[2:4])
	}
	if len(digits) == 6 {
		sec, _ = strconv.Atoi(digits[4:6])
	}
	if h > 18 || m > 59 || sec > 59 || (h == 18 && (m > 0 || sec > 0)) {
		return 0, false
	}
	return sign * (h*3600 + m*60 + sec), true
}

// parseZone accepts an offset or a region name.
func parseZone(s string) (zoneSpec, error) {
	if off, ok := parseOffset(s); ok {
		return fixedZone(off), nil
	}
	loc, err := time.LoadLocation(s)
	if err != nil || s == "" || s == "Local" {
		return zoneSpec{}, fmt.Errorf("unknown time zone %q", s)
	}
	return zoneSpec{loc: loc, named: true}, nil
}

// offsetAt returns the zone's offset at the given local wall-clock time.
func (z zoneSpec) offsetAt(wall time.Time) int {
	t := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), z.loc)
	_, off := t.Zone()
	return off
}

// at places a local wall-clock time in the zone.
func (z zoneSpec) at(wall time.Time) DateTime {
	return DateTime{t: time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), z.loc), named: z.named}
}

// zone returns the zone of a DateTime.
func (t DateTime) zone() zoneSpec {
	return zoneSpec{loc: t.t.Location(), named: t.named}
}

// Offset is the UTC offset in seconds.
func (t DateTime) Offset() int { _, off := t.t.Zone(); return off }

// ZoneName is the region name, or the offset rendering for fixed offsets.
func (t DateTime) ZoneName() string {
	if t.named {
		return t.t.Location().String()
	}
	return fmtOffset(t.Offset())
}
