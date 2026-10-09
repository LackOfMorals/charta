// Package spatial implements the Cypher POINT type: cartesian and WGS-84
// (geographic) points in two or three dimensions, point construction from a
// map, distance and bounding-box functions, and rendering. It depends only on
// the standard library.
package spatial

import (
	"fmt"
	"math"
	"strings"
)

// Coordinate reference system identifiers (SRIDs).
const (
	SRIDCartesian   = 7203
	SRIDCartesian3D = 9157
	SRIDWGS84       = 4326
	SRIDWGS84_3D    = 4979
)

// earthRadius is the radius, in metres, Neo4j uses for WGS-84 distances.
const earthRadius = 6378140.0

// Point is a point in a coordinate reference system. Z is meaningful only for
// three-dimensional systems.
type Point struct {
	SRID    int
	X, Y, Z float64
}

// Is3D reports whether the point has a third coordinate.
func (p Point) Is3D() bool { return p.SRID == SRIDCartesian3D || p.SRID == SRIDWGS84_3D }

// Geographic reports whether the point is in a WGS-84 system.
func (p Point) Geographic() bool { return p.SRID == SRIDWGS84 || p.SRID == SRIDWGS84_3D }

// CRS is the name of the point's coordinate reference system.
func (p Point) CRS() string {
	switch p.SRID {
	case SRIDCartesian:
		return "cartesian"
	case SRIDCartesian3D:
		return "cartesian-3d"
	case SRIDWGS84:
		return "wgs-84"
	}
	return "wgs-84-3d"
}

func fmtCoord(f float64) string {
	s := fmt.Sprintf("%v", f)
	if !strings.ContainsAny(s, ".eEN") && !strings.Contains(s, "Inf") {
		s += ".0"
	}
	return s
}

// String renders the point the way Cypher does, e.g.
// point({srid: 7203, x: 1.0, y: 2.0}).
func (p Point) String() string {
	s := fmt.Sprintf("point({srid: %d, x: %s, y: %s", p.SRID, fmtCoord(p.X), fmtCoord(p.Y))
	if p.Is3D() {
		s += ", z: " + fmtCoord(p.Z)
	}
	return s + "})"
}

// FromMap builds a point from the entries of a map: x/y[/z] or
// longitude/latitude[/height], with an optional crs or srid.
func FromMap(m map[string]any) (Point, error) {
	num := func(key string) (float64, bool, error) {
		v, ok := m[key]
		if !ok {
			return 0, false, nil
		}
		switch x := v.(type) {
		case int64:
			return float64(x), true, nil
		case float64:
			return x, true, nil
		}
		return 0, false, fmt.Errorf("point(): %s must be a number", key)
	}
	for k := range m {
		switch k {
		case "x", "y", "z", "longitude", "latitude", "height", "crs", "srid":
		default:
			return Point{}, fmt.Errorf("point(): unknown key %q", k)
		}
	}
	x, hasX, err := num("x")
	if err != nil {
		return Point{}, err
	}
	y, hasY, err := num("y")
	if err != nil {
		return Point{}, err
	}
	z, hasZ, err := num("z")
	if err != nil {
		return Point{}, err
	}
	lon, hasLon, err := num("longitude")
	if err != nil {
		return Point{}, err
	}
	lat, hasLat, err := num("latitude")
	if err != nil {
		return Point{}, err
	}
	h, hasH, err := num("height")
	if err != nil {
		return Point{}, err
	}
	geographicKeys := hasLon || hasLat || hasH
	cartesianKeys := hasX || hasY || hasZ
	if geographicKeys && cartesianKeys {
		return Point{}, fmt.Errorf("point(): cannot mix x/y/z with longitude/latitude/height")
	}

	srid := 0
	if raw, ok := m["srid"]; ok {
		n, isInt := raw.(int64)
		if !isInt {
			return Point{}, fmt.Errorf("point(): srid must be an integer")
		}
		srid = int(n)
	}
	if raw, ok := m["crs"]; ok {
		name, isStr := raw.(string)
		if !isStr {
			return Point{}, fmt.Errorf("point(): crs must be a string")
		}
		switch strings.ToLower(name) {
		case "cartesian":
			srid = SRIDCartesian
		case "cartesian-3d":
			srid = SRIDCartesian3D
		case "wgs-84":
			srid = SRIDWGS84
		case "wgs-84-3d":
			srid = SRIDWGS84_3D
		default:
			return Point{}, fmt.Errorf("point(): unknown coordinate reference system %q", name)
		}
	}
	if srid != 0 {
		switch srid {
		case SRIDCartesian, SRIDCartesian3D, SRIDWGS84, SRIDWGS84_3D:
		default:
			return Point{}, fmt.Errorf("point(): unknown SRID %d", srid)
		}
	}

	p := Point{}
	switch {
	case geographicKeys:
		if !hasLon || !hasLat {
			return Point{}, fmt.Errorf("point(): a geographic point needs longitude and latitude")
		}
		p = Point{SRID: SRIDWGS84, X: lon, Y: lat}
		if hasH {
			p.SRID, p.Z = SRIDWGS84_3D, h
		}
		if srid != 0 && srid != p.SRID {
			return Point{}, fmt.Errorf("point(): coordinates do not match the coordinate reference system")
		}
	default:
		if !hasX || !hasY {
			return Point{}, fmt.Errorf("point(): a point needs x and y (or longitude and latitude)")
		}
		p = Point{SRID: SRIDCartesian, X: x, Y: y}
		if hasZ {
			p.SRID, p.Z = SRIDCartesian3D, z
		}
		if srid != 0 {
			p.SRID = srid
			if p.Is3D() != hasZ {
				return Point{}, fmt.Errorf("point(): coordinates do not match the coordinate reference system")
			}
		}
	}
	if p.Geographic() {
		if p.Y < -90 || p.Y > 90 {
			return Point{}, fmt.Errorf("point(): latitude %v is outside [-90, 90]", p.Y)
		}
		if p.X < -180 || p.X > 180 {
			return Point{}, fmt.Errorf("point(): longitude %v is outside [-180, 180]", p.X)
		}
	}
	return p, nil
}

// Property returns a component of the point: x, y, z, longitude, latitude,
// height, crs or srid. ok is false for a name that is not a point property; a
// component the point lacks (z of a 2D point) is nil.
func (p Point) Property(name string) (v any, ok bool) {
	switch name {
	case "x":
		return p.X, true
	case "y":
		return p.Y, true
	case "z":
		if p.Is3D() {
			return p.Z, true
		}
		return nil, true
	case "longitude":
		if p.Geographic() {
			return p.X, true
		}
		return nil, true
	case "latitude":
		if p.Geographic() {
			return p.Y, true
		}
		return nil, true
	case "height":
		if p.SRID == SRIDWGS84_3D {
			return p.Z, true
		}
		return nil, true
	case "crs":
		return p.CRS(), true
	case "srid":
		return int64(p.SRID), true
	}
	return nil, false
}

// Distance is the distance between two points: euclidean for cartesian
// points, great-circle (haversine) metres for WGS-84 points, with the height
// difference folded in for 3D points. ok is false if the points are in
// different coordinate reference systems.
func Distance(a, b Point) (d float64, ok bool) {
	if a.SRID != b.SRID {
		return 0, false
	}
	if !a.Geographic() {
		d2 := (a.X-b.X)*(a.X-b.X) + (a.Y-b.Y)*(a.Y-b.Y)
		if a.Is3D() {
			d2 += (a.Z - b.Z) * (a.Z - b.Z)
		}
		return math.Sqrt(d2), true
	}
	rad := math.Pi / 180
	lat1, lat2 := a.Y*rad, b.Y*rad
	dLat, dLon := lat2-lat1, (b.X-a.X)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	d = 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
	if a.Is3D() {
		d = math.Sqrt(d*d + (a.Z-b.Z)*(a.Z-b.Z))
	}
	return d, true
}

// WithinBBox reports whether p lies in the box with the given lower-left and
// upper-right corners. For geographic points the box may cross the
// antimeridian (lower-left longitude greater than upper-right). ok is false if
// the points are in different coordinate reference systems.
func WithinBBox(p, lowerLeft, upperRight Point) (within, ok bool) {
	if p.SRID != lowerLeft.SRID || p.SRID != upperRight.SRID {
		return false, false
	}
	inY := p.Y >= lowerLeft.Y && p.Y <= upperRight.Y
	var inX bool
	if p.Geographic() && lowerLeft.X > upperRight.X {
		inX = p.X >= lowerLeft.X || p.X <= upperRight.X
	} else {
		inX = p.X >= lowerLeft.X && p.X <= upperRight.X
	}
	inZ := true
	if p.Is3D() {
		inZ = p.Z >= lowerLeft.Z && p.Z <= upperRight.Z
	}
	return inX && inY && inZ, true
}

// Compare orders points for ORDER BY: by SRID, then x, y and z.
func Compare(a, b Point) int {
	switch {
	case a.SRID != b.SRID:
		if a.SRID < b.SRID {
			return -1
		}
		return 1
	case a.X != b.X:
		return cmp(a.X, b.X)
	case a.Y != b.Y:
		return cmp(a.Y, b.Y)
	}
	return cmp(a.Z, b.Z)
}

func cmp(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
