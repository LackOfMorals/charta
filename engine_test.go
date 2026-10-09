package charta_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LackOfMorals/charta"
)

func TestUnsupportedConstructIsErrUnsupportedCypher(t *testing.T) {
	db := openMemDB(t)
	_, err := db.RunQuery(context.Background(), "SHOW USERS", nil)
	var target *charta.ErrUnsupportedCypher
	if !errors.As(err, &target) {
		t.Fatalf("got %T %v, want *ErrUnsupportedCypher", err, err)
	}
}

func TestMaxPathHopsOption(t *testing.T) {
	ctx := context.Background()
	db, err := charta.Open(":memory:", charta.WithMaxPathHops(2))
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
	} else if _, ok := v.(charta.Date); !ok {
		t.Errorf("d is %T, want charta.Date", v)
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
		got = append(got, v.(charta.Date).String())
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

func TestLoadCSV(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("people.csv", "name,age,city\nAlice,30,London\nBob,,Paris\n")
	write("semi.csv", "a;b\n1;2\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.csv"), []byte("x\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Disabled without an import directory.
	plain := openMemDB(t)
	if _, err := plain.RunQuery(ctx, "LOAD CSV FROM 'file:///people.csv' AS row RETURN row", nil); err == nil {
		t.Fatal("LOAD CSV must be disabled by default")
	}

	db, err := charta.Open(":memory:", charta.WithImportDirectory(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	collect := func(q string) []string {
		t.Helper()
		res, err := db.RunQuery(ctx, q, nil)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		var out []string
		for _, r := range recs {
			out = append(out, fmt.Sprint(r.Values()...))
		}
		return out
	}
	got := collect("LOAD CSV WITH HEADERS FROM 'file:///people.csv' AS row RETURN row.name, row.age, row.city")
	if strings.Join(got, "|") != "Alice30London|Bob<nil>Paris" {
		t.Errorf("with headers: %v", got)
	}
	got = collect("LOAD CSV FROM 'file:///people.csv' AS row RETURN size(row), row[0], linenumber()")
	if strings.Join(got, "|") != "3name1|3Alice2|3Bob3" {
		t.Errorf("without headers: %v", got)
	}
	got = collect("LOAD CSV WITH HEADERS FROM 'semi.csv' AS row FIELDTERMINATOR ';' RETURN toInteger(row.a) + toInteger(row.b)")
	if strings.Join(got, "|") != "3" {
		t.Errorf("field terminator: %v", got)
	}
	// Create nodes from a file.
	if _, err := db.RunQuery(ctx, "LOAD CSV WITH HEADERS FROM 'file:///people.csv' AS row CREATE (:P {name: row.name})", nil); err != nil {
		t.Fatal(err)
	}
	if got := collect("MATCH (p:P) RETURN count(p)"); got[0] != "2" {
		t.Errorf("created %v", got)
	}
	// Nothing outside the import directory is readable.
	for _, u := range []string{"file:///../secret.csv", "../secret.csv", "file:///../../etc/passwd"} {
		res, err := db.RunQuery(ctx, "LOAD CSV FROM '"+u+"' AS row RETURN row", nil)
		if err == nil {
			if recs, _ := res.Collect(ctx); len(recs) > 0 {
				t.Errorf("%s escaped the import directory", u)
			}
		}
	}
	if _, err := db.RunQuery(ctx, "LOAD CSV FROM 'https://example.com/x.csv' AS row RETURN row", nil); err == nil {
		t.Error("remote URLs must be rejected")
	}
}

func TestLoadCSVRejectsSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "s.csv"), []byte("x\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	db, err := charta.Open(":memory:", charta.WithImportDirectory(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.RunQuery(ctx, "LOAD CSV FROM 'file:///link/s.csv' AS row RETURN row", nil); err == nil {
		t.Error("a symlink out of the import directory must not be followed")
	}
}
