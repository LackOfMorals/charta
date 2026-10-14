package api

import (
	"strings"

	"github.com/LackOfMorals/charta/cypher/temporal"
)

// temporalString renders a temporal value the way a Neo4j server does in
// a Query API response. charta's String is the shortest Cypher form
// ("12:30", "10:30:15.500"); Neo4j always writes the seconds, writes a
// fraction with no trailing zeros ("15.5"), and in Plain JSON omits the
// "[Region]" suffix of a zoned datetime (Typed JSON keeps it).
func temporalString(v temporal.Value, typed bool) string {
	s := v.String()
	switch v.(type) {
	case temporal.LocalTime, temporal.Time:
		return normalizeClock(s)
	case temporal.LocalDateTime, temporal.DateTime:
		zone := ""
		if i := strings.IndexByte(s, '['); i >= 0 {
			s, zone = s[:i], s[i:]
		}
		// The date part never contains a 'T', so the first one separates it
		// from the time (a region name, which can, has been cut off).
		if i := strings.IndexByte(s, 'T'); i >= 0 {
			s = s[:i+1] + normalizeClock(s[i+1:])
		}
		if typed {
			s += zone
		}
		return s
	}
	return s
}

// normalizeClock rewrites "HH:MM[:SS[.fff]]<offset>" with seconds always
// present and no trailing zeros in the fraction. The offset ("Z", "+01:00")
// is passed through.
func normalizeClock(s string) string {
	if len(s) < 5 {
		return s
	}
	hm, rest := s[:5], s[5:]
	sec := ":00"
	if strings.HasPrefix(rest, ":") && len(rest) >= 3 {
		sec, rest = rest[:3], rest[3:]
		if strings.HasPrefix(rest, ".") {
			n := 1
			for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
				n++
			}
			if frac := strings.TrimRight(rest[1:n], "0"); frac != "" {
				sec += "." + frac
			}
			rest = rest[n:]
		}
	}
	return hm + sec + rest
}
