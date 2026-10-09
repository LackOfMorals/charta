package graphlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LackOfMorals/graphlite/v2"
)

func TestUnsupportedConstructIsErrUnsupportedCypher(t *testing.T) {
	db := openMemDB(t)
	_, err := db.RunQuery(context.Background(), "SHOW USERS", nil)
	var target *graphlite.ErrUnsupportedCypher
	if !errors.As(err, &target) {
		t.Fatalf("got %T %v, want *ErrUnsupportedCypher", err, err)
	}
}

func TestMaxPathHopsOption(t *testing.T) {
	ctx := context.Background()
	db, err := graphlite.Open(":memory:", graphlite.WithMaxPathHops(2))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.RunQuery(ctx, "CREATE (:N)-[:R]->(:N)", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunQuery(ctx, "MATCH (a:N)-[:R*1..5]->(b) RETURN b", nil); err == nil {
		t.Error("explicit bound above the cap should fail")
	}
}

func TestTemporalValuesRoundTripThroughStorage(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	if _, err := db.RunQuery(ctx, `CREATE (:E {
		d: date('2024-02-29'), t: time('10:15:30+02:00'), lt: localtime('10:15'),
		ldt: localdatetime('2024-02-29T10:15'), dt: datetime('2024-02-29T10:15:30[Europe/Stockholm]'),
		dur: duration('P1Y2M3DT4H'), ds: [date('2024-01-01'), date('2024-12-31')]})`, nil); err != nil {
		t.Fatal(err)
	}
	res, err := db.RunQuery(ctx, `MATCH (e:E) RETURN e.d AS d, e.t AS t, e.lt AS lt, e.ldt AS ldt, e.dt AS dt, e.dur AS dur, e.ds AS ds,
		e.d + duration('P1D') AS next, e.dt.timezone AS tz`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := res.Single(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"d": "2024-02-29", "t": "10:15:30+02:00", "lt": "10:15", "ldt": "2024-02-29T10:15",
		"dt": "2024-02-29T10:15:30+01:00[Europe/Stockholm]", "dur": "P1Y2M3DT4H", "next": "2024-03-01",
	}
	for k, w := range want {
		v, _ := rec.Get(k)
		s, ok := v.(interface{ String() string })
		if !ok || s.String() != w {
			t.Errorf("%s = %v (%T), want %s", k, v, v, w)
		}
	}
	if v, _ := rec.Get("d"); v == nil {
		t.Fatal("missing d")
	} else if _, ok := v.(graphlite.Date); !ok {
		t.Errorf("d is %T, want graphlite.Date", v)
	}
	if v, _ := rec.Get("tz"); v != "Europe/Stockholm" {
		t.Errorf("tz = %v", v)
	}
	if v, _ := rec.Get("ds"); len(v.([]any)) != 2 {
		t.Errorf("ds = %v", v)
	}
}

func TestTemporalComparisonAndOrdering(t *testing.T) {
	ctx := context.Background()
	db := openMemDB(t)
	res, err := db.RunQuery(ctx, `UNWIND [date('2020-03-01'), date('2019-12-31'), date('2020-01-15')] AS d
		RETURN d ORDER BY d`, nil)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := res.Collect(ctx)
	var got []string
	for _, r := range recs {
		v, _ := r.Get("d")
		got = append(got, v.(graphlite.Date).String())
	}
	if want := "2019-12-31 2020-01-15 2020-03-01"; strings.Join(got, " ") != want {
		t.Errorf("ordered %v, want %s", got, want)
	}
	res, err = db.RunQuery(ctx, `RETURN date('2020-01-01') < date('2020-01-02') AS lt, date('2020-01-01') = localdatetime('2020-01-01T00:00') AS eq`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := res.Single(ctx)
	if lt, _ := rec.Get("lt"); lt != true {
		t.Errorf("lt = %v", lt)
	}
	if eq, _ := rec.Get("eq"); eq != false {
		t.Errorf("a date never equals a datetime, got %v", eq)
	}
}
