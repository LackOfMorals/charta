package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/graingraph/graingraph/cypher/spatial"
)

// wktOf renders a point in the WKT form used by the Query API, e.g.
// "SRID=7203;POINT (1.2 3.4)" or "SRID=9157;POINT Z (1.0 2.0 3.0)".
func wktOf(p spatial.Point) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "SRID=%d;POINT", p.SRID)
	if p.Is3D() {
		sb.WriteString(" Z")
	}
	sb.WriteString(" (")
	sb.WriteString(wktCoord(p.X))
	sb.WriteByte(' ')
	sb.WriteString(wktCoord(p.Y))
	if p.Is3D() {
		sb.WriteByte(' ')
		sb.WriteString(wktCoord(p.Z))
	}
	sb.WriteString(")")
	return sb.String()
}

// wktCoord renders one coordinate in shortest form, adding ".0" to
// integral values so that the output always looks like a WKT float.
func wktCoord(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// parseWKT parses the WKT point form accepted as a Typed JSON Point
// parameter. Both the "SRID=<n>;" prefix and the "Z" axis keyword are
// optional.
func parseWKT(s string) (spatial.Point, error) {
	rest := strings.TrimSpace(s)
	srid := spatial.SRIDCartesian
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		prefix := rest[:i]
		if !strings.HasPrefix(strings.ToLower(prefix), "srid=") {
			return spatial.Point{}, fmt.Errorf("point: expected SRID prefix, got %q", prefix)
		}
		n, err := strconv.Atoi(prefix[len("srid="):])
		if err != nil {
			return spatial.Point{}, fmt.Errorf("point: invalid SRID %q", prefix)
		}
		switch n {
		case spatial.SRIDCartesian, spatial.SRIDCartesian3D,
			spatial.SRIDWGS84, spatial.SRIDWGS84_3D:
			srid = n
		default:
			return spatial.Point{}, fmt.Errorf("point: unknown SRID %d", n)
		}
		rest = rest[i+1:]
	}
	upper := strings.ToUpper(strings.TrimSpace(rest))
	if !strings.HasPrefix(upper, "POINT") {
		return spatial.Point{}, fmt.Errorf("point: expected POINT, got %q", rest)
	}
	upper = strings.TrimSpace(upper[len("POINT"):])
	z := false
	if strings.HasPrefix(upper, "Z") {
		z = true
		upper = strings.TrimSpace(upper[1:])
	}
	if !strings.HasPrefix(upper, "(") || !strings.HasSuffix(upper, ")") {
		return spatial.Point{}, fmt.Errorf("point: missing coordinates in %q", s)
	}
	coords := strings.Fields(upper[1 : len(upper)-1])
	nums := make([]float64, 0, len(coords))
	for _, c := range coords {
		f, err := strconv.ParseFloat(c, 64)
		if err != nil {
			return spatial.Point{}, fmt.Errorf("point: invalid coordinate %q", c)
		}
		nums = append(nums, f)
	}
	p := spatial.Point{SRID: srid}
	if z {
		if srid != spatial.SRIDCartesian3D && srid != spatial.SRIDWGS84_3D {
			// The prefix did not name a 3D SRID; derive it.
			switch srid {
			case spatial.SRIDCartesian:
				p.SRID = spatial.SRIDCartesian3D
			case spatial.SRIDWGS84:
				p.SRID = spatial.SRIDWGS84_3D
			}
		}
		if len(nums) != 3 {
			return spatial.Point{}, fmt.Errorf("point: expected 3 coordinates for POINT Z, got %d", len(nums))
		}
		p.X, p.Y, p.Z = nums[0], nums[1], nums[2]
		return p, nil
	}
	if srid == spatial.SRIDCartesian3D || srid == spatial.SRIDWGS84_3D {
		return spatial.Point{}, fmt.Errorf("point: SRID %d requires POINT Z", srid)
	}
	if len(nums) != 2 {
		return spatial.Point{}, fmt.Errorf("point: expected 2 coordinates, got %d", len(nums))
	}
	p.X, p.Y = nums[0], nums[1]
	return p, nil
}
