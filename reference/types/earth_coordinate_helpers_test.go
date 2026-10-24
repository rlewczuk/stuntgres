package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func earthCoordinateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func earthCoordinateBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func earthCoordinateFloat(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(want) {
		if !math.IsNaN(got) {
			t.Errorf("got %.17g, want NaN", got)
		}
		return
	}
	if want == 0 {
		if got != 0 || math.Signbit(got) != math.Signbit(want) {
			t.Errorf("got %g sign=%v, want %g sign=%v", got, math.Signbit(got), want, math.Signbit(want))
		}
		return
	}
	if math.IsInf(got, 0) || math.IsNaN(got) || math.Abs(got-want) > tolerance {
		t.Errorf("got %.17g, want %.17g tolerance %g", got, want, tolerance)
	}
}

// Numeric witnesses scan the public FLOAT8 result directly. Coordinate
// expectations use the stated degree tolerance; string expectations are fixed
// released five-decimal witnesses, compared only after rounding the observed
// value. No expected value comes from native output or production kernels.
func earthCoordinateNumbers(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, columns []string, want [][]any, tolerance []float64) {
	t.Helper()
	for _, route := range []string{"direct", "fresh", "held"} {
		var executor fixture.Executor = c
		var stmt *sql.Stmt
		if route != "direct" {
			var err error
			stmt, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(route, q, err)
			}
			defer stmt.Close()
			executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		repeats := 1
		if route == "held" {
			repeats = 2
		}
		for range repeats {
			rows, err := executor.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(route, q, err)
			}
			func() {
				defer rows.Close()
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(meta) != len(columns) {
					t.Fatalf("%s metadata columns=%d want %d", route, len(meta), len(columns))
				}
				for i, col := range meta {
					typ, scan := "FLOAT8", reflect.TypeFor[float64]()
					if columns[i] == "id" {
						typ, scan = "INT4", reflect.TypeFor[int64]()
						if b.PG {
							scan = reflect.TypeFor[int32]()
						}
					}
					if col.Name() != columns[i] || col.DatabaseTypeName() != typ || col.ScanType() != scan {
						t.Fatalf("%s column %d name/type/scan=%q/%q/%v want %q/%q/%v", route, i, col.Name(), col.DatabaseTypeName(), col.ScanType(), columns[i], typ, scan)
					}
				}
				index := 0
				for rows.Next() {
					if index >= len(want) {
						t.Fatalf("%s extra row %d", route, index)
					}
					values, dest := make([]any, len(columns)), make([]any, len(columns))
					for i := range dest {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, expected := range want[index] {
						if expected == nil {
							if values[i] != nil {
								t.Fatalf("%s row %d column %s=%v want NULL", route, index, columns[i], values[i])
							}
							continue
						}
						if integer, ok := expected.(int64); ok {
							if got, ok := values[i].(int64); !ok || got != integer {
								t.Fatalf("%s id=%v want %d", route, values[i], integer)
							}
							continue
						}
						got, ok := values[i].(float64)
						if !ok {
							t.Fatalf("%s row %d column %s raw=%T want float64", route, index, columns[i], values[i])
						}
						if rounded, ok := expected.(string); ok {
							if actual := fmt.Sprintf("%.5f", got); actual != rounded {
								t.Errorf("%s row %d column %s raw=%.17g rounded=%s want %s", route, index, columns[i], got, actual, rounded)
							}
						} else {
							earthCoordinateFloat(t, got, expected.(float64), tolerance[i])
						}
					}
					index++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if index != len(want) {
					t.Fatalf("%s rows=%d want %d", route, index, len(want))
				}
			}()
		}
	}
}

func earthCoordinateStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE earth_coordinate_input(id integer,lat float8,lon float8,d float8)",
		"CREATE VIEW earth_coordinate_view AS SELECT id,earth() AS r,ll_to_earth(lat,lon) AS p,latitude(ll_to_earth(lat,lon)) AS lat,longitude(ll_to_earth(lat,lon)) AS lon,gc_to_sec(d) AS chord,sec_to_gc(d) AS arc FROM earth_coordinate_input",
		"CREATE TABLE earth_coordinate_defaults(id integer,r float8 DEFAULT earth(),p earth DEFAULT ll_to_earth(0,0),lat float8 DEFAULT latitude(ll_to_earth(0,0)),lon float8 DEFAULT longitude(ll_to_earth(0,0)),chord float8 DEFAULT gc_to_sec(0),arc float8 DEFAULT sec_to_gc(0))",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_coordinate_input VALUES (1,0,0,0),(2,45,90,10000),(3,NULL,NULL,NULL)", 3))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_coordinate_defaults(id) VALUES (1)", 1))
}

func earthCoordinateStoredVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	q := "SELECT id,r,lat,lon,chord,arc FROM earth_coordinate_view ORDER BY id"
	earthCoordinateNumbers(t, p, b, c, q, []string{"id", "r", "lat", "lon", "chord", "arc"}, [][]any{
		{int64(1), float64(6378168), float64(0), float64(0), "0.00000", "0.00000"},
		{int64(2), float64(6378168), float64(45), float64(90), "9999.99898", "10000.00102"},
		{int64(3), float64(6378168), nil, nil, nil, nil},
	}, []float64{0, 0, 1e-10, 1e-10, 0, 0})
	q = "SELECT id,r,latitude(p) AS plat,longitude(p) AS plon,lat,lon,chord,arc FROM earth_coordinate_defaults ORDER BY id"
	earthCoordinateBoth(t, p, b, c, fixture.Select(q, []string{"id", "r", "plat", "plon", "lat", "lon", "chord", "arc"}, []string{"INT4", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(1), float64(6378168), float64(0), float64(0), float64(0), float64(0), float64(0), float64(0)}}))
	s := earthSelect(b, "SELECT p FROM earth_coordinate_defaults ORDER BY id", []string{"p"}, [][]any{{"(6378168, 0, 0)"}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, []string{"p"}, []bool{true}) }
	earthCoordinateBoth(t, p, b, c, s)
}
