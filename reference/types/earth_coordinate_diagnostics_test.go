package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete portable records and stages were observed by the root on pinned
// PostgreSQL 18.6. All fields not explicitly assigned must remain empty.
func earthCoordinateBodyError(name, code, message string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Context = `SQL function "` + name + `" statement 1`
	return want
}

func earthCoordinateMissing(signature, position string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	want.Position = position
	return want
}

func earthCoordinateError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, route string, args []any, want fixture.Diagnostic, prepareFailure bool, valid []any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	if route == "fresh" {
		s.Prepared = true
		if prepareFailure {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		return
	}
	if route == "direct" {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		return
	}
	stmt, err := c.PrepareContext(p.Ctx, q)
	if prepareFailure {
		if stmt != nil {
			stmt.Close()
		}
		if err == nil {
			t.Fatal("held Prepare succeeded, want failure")
		}
		fixture.AssertDiagnostic(t, err, &want)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	if valid != nil {
		rows, err := stmt.QueryContext(p.Ctx, valid...)
		if err != nil {
			t.Fatal("valid reuse after held failure", err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("valid held reuse returned %d rows, want 1", count)
		}
	}
}

func TestReferenceEarthCoordinateDiagnostics(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ q, signature string }{
				{"SELECT earth()", "earth()"}, {"SELECT ll_to_earth(0,0)", "ll_to_earth(integer, integer)"},
				{"SELECT sec_to_gc(0)", "sec_to_gc(integer)"}, {"SELECT gc_to_sec(0)", "gc_to_sec(integer)"},
				{"SELECT latitude(NULL)", "latitude(unknown)"}, {"SELECT longitude(NULL)", "longitude(unknown)"},
			} {
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.q, route, nil, earthCoordinateMissing(tc.signature, "8"), true, nil)
				}
			}
		})
	})
	underflow := func(fn string) fixture.Diagnostic {
		return earthCoordinateBodyError(fn, "22003", "value out of range: underflow")
	}
	infinity := earthCoordinateBodyError("ll_to_earth", "22003", "input is out of range")
	nan := earthCheckDiagnostic("public", "on_surface", true)
	nan.Context = `SQL function "ll_to_earth" statement 1`
	malformed := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
	malformed.Position = "31"
	overflow := fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`)
	overflow.Position = "18"
	cases := []struct {
		name, literal, bound         string
		args, valid                  []any
		literalWant, boundWant       fixture.Diagnostic
		literalPrepare, boundPrepare bool
	}{
		{"sec-underflow", "SELECT sec_to_gc('5e-324'::float8)", "SELECT sec_to_gc($1::float8)", []any{"5e-324"}, []any{"0"}, underflow("sec_to_gc"), underflow("sec_to_gc"), false, false},
		{"gc-underflow", "SELECT gc_to_sec('5e-324'::float8)", "SELECT gc_to_sec($1::float8)", []any{"5e-324"}, []any{"0"}, underflow("gc_to_sec"), underflow("gc_to_sec"), false, false},
		{"lat-underflow", "SELECT ll_to_earth('5e-324'::float8,0)", "SELECT ll_to_earth($1::float8,0)", []any{"5e-324"}, []any{"0"}, underflow("ll_to_earth"), underflow("ll_to_earth"), false, false},
		{"lon-underflow", "SELECT ll_to_earth(0,'5e-324'::float8)", "SELECT ll_to_earth(0,$1::float8)", []any{"5e-324"}, []any{"0"}, underflow("ll_to_earth"), underflow("ll_to_earth"), false, false},
		{"lat-infinity", "SELECT ll_to_earth('Infinity'::float8,0)", "SELECT ll_to_earth($1::float8,0)", []any{"Infinity"}, []any{"0"}, infinity, infinity, false, false},
		{"lon-negative-infinity", "SELECT ll_to_earth(0,'-Infinity'::float8)", "SELECT ll_to_earth(0,$1::float8)", []any{"-Infinity"}, []any{"0"}, infinity, infinity, false, false},
		{"nan-lat", "SELECT ll_to_earth('NaN'::float8,0)", "SELECT ll_to_earth($1::float8,0)", []any{"NaN"}, []any{"0"}, nan, nan, false, false},
		{"nan-lon", "SELECT ll_to_earth(0,'NaN'::float8)", "SELECT ll_to_earth(0,$1::float8)", []any{"NaN"}, []any{"0"}, nan, nan, false, false},
		{"not-3d", "SELECT latitude('(6378168,0,0,0)'::earth)", "SELECT latitude($1::earth)", []any{"(6378168,0,0,0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "not_3d", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "not_3d", true), "(6378168,0,0,0)", 1), false, false},
		{"not-point", "SELECT longitude('(6378168,0),(6378168,1)'::earth)", "SELECT longitude($1::earth)", []any{"(6378168,0),(6378168,1)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "not_point", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "not_point", true), "(6378168,0),(6378168,1)", 1), false, false},
		{"on-surface", "SELECT latitude('(0)'::earth)", "SELECT latitude($1::earth)", []any{"(0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "on_surface", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "(0)", 1), false, false},
		{"malformed-multibyte", "SELECT 'é' AS marker,latitude('(6378168,)'::earth)", "SELECT 'é' AS marker,latitude($1::earth)", []any{"(6378168,)"}, []any{"(6378168)"}, malformed, cubeParameterDiagnostic(cubeSyntaxDiagnostic(`syntax error at or near ")"`), "(6378168,)", 1), true, false},
		{"overflow", "SELECT sec_to_gc('1e999'::float8)", "SELECT sec_to_gc($1::float8)", []any{"1e999"}, []any{"0"}, overflow, cubeParameterDiagnostic(fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`), "1e999", 1), true, false},
		{"arity", "SELECT earth(1)", "SELECT earth($1::float8)", []any{1}, nil, earthCoordinateMissing("earth(integer)", "8"), earthCoordinateMissing("earth(double precision)", "8"), true, true},
		{"wrong-type", "SELECT latitude(true)", "SELECT latitude($1::boolean)", []any{true}, nil, earthCoordinateMissing("latitude(boolean)", "8"), earthCoordinateMissing("latitude(boolean)", "8"), true, true},
		{"qualified-missing", "SELECT pg_catalog.earth()", "SELECT pg_catalog.earth()", nil, nil, earthCoordinateMissing("pg_catalog.earth()", "8"), earthCoordinateMissing("pg_catalog.earth()", "8"), true, true},
		{"decoration", "SELECT earth(DISTINCT 1)", "SELECT earth(DISTINCT $1::float8)", []any{1}, nil, earthCoordinateMissing("earth(integer)", "8"), earthCoordinateMissing("earth(double precision)", "8"), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				earthInstall(t, p, b, c)
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.literal, route, nil, tc.literalWant, tc.literalPrepare, nil)
					earthCoordinateError(t, p, b, c, tc.bound, route, tc.args, tc.boundWant, tc.boundPrepare, tc.valid)
				}
				h := earthHeld(t, p, c, "SELECT latitude(ll_to_earth($1::float8,$2::float8)) AS lat")
				defer h.Statement.Close()
				for _, xy := range [][2]float64{{0, 0}, {45, 90}} {
					var got float64
					if err := h.Statement.QueryRowContext(p.Ctx, xy[0], xy[1]).Scan(&got); err != nil {
						t.Fatal(err)
					}
					earthCoordinateFloat(t, got, xy[0], 1e-10)
				}
			})
		})
	}
	t.Run("nan-admission", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			for _, tc := range []struct {
				literal, bound string
				args           []any
				names          []string
				want           []any
			}{
				{"SELECT ll_to_earth(90,'NaN'::float8),latitude(ll_to_earth(90,'NaN'::float8)),longitude(ll_to_earth(90,'NaN'::float8))", "SELECT ll_to_earth(90,$1::float8),latitude(ll_to_earth(90,$1::float8)),longitude(ll_to_earth(90,$1::float8))", []any{"NaN"}, []string{"ll_to_earth", "latitude", "longitude"}, []any{"(NaN, NaN, 6378168)", float64(90), math.NaN()}},
				{"SELECT latitude('(6378168,0,NaN)'::earth)", "SELECT latitude($1::earth)", []any{"(6378168,0,NaN)"}, []string{"latitude"}, []any{float64(90)}},
				{"SELECT longitude('(6378168,NaN)'::earth)", "SELECT longitude($1::earth)", []any{"(6378168,NaN)"}, []string{"longitude"}, []any{math.NaN()}},
			} {
				for _, q := range []string{tc.literal, tc.bound} {
					var args []any
					if q == tc.bound {
						args = tc.args
					}
					for _, route := range []string{"direct", "fresh", "held"} {
						var executor fixture.Executor = c
						var stmt *sql.Stmt
						if route != "direct" {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						count := 1
						if route == "held" {
							count = 2
						}
						for range count {
							earthCoordinateNaNRows(t, p, b, executor, q, args, tc.names, tc.want)
						}
						if stmt != nil {
							stmt.Close()
						}
					}
				}
			}
		})
	})
	t.Run("atomicity", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE earth_coordinate_atomic(id integer,p earth)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_coordinate_atomic VALUES (1,ll_to_earth(0,0))", 1))
			for _, route := range []string{"direct", "fresh", "held"} {
				for _, parameter := range []bool{false, true} {
					q := "INSERT INTO earth_coordinate_atomic VALUES (2,ll_to_earth(0,0)),(3,ll_to_earth('Infinity'::float8,0)) RETURNING id"
					var args []any
					if parameter {
						q = "INSERT INTO earth_coordinate_atomic VALUES (2,ll_to_earth(0,0)),(3,ll_to_earth($1::float8,0)) RETURNING id"
						args = []any{"Infinity"}
					}
					earthCoordinateError(t, p, b, c, q, route, args, infinity, false, nil)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, earthSelect(b, "SELECT id,p FROM earth_coordinate_atomic ORDER BY id", []string{"id", "p"}, [][]any{{int64(1), "(6378168, 0, 0)"}}))
				}
			}
		})
	})
}

func earthCoordinateNaNRows(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, q string, args []any, names []string, want []any) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != len(names) {
		t.Fatalf("columns=%d want %d", len(cols), len(names))
	}
	for i, col := range cols {
		if col.Name() != names[i] {
			t.Fatalf("column=%q want %q", col.Name(), names[i])
		}
		if names[i] == "ll_to_earth" {
			cubeColumnMetadata(t, b.PG, cols[i:i+1], names[i:i+1], nil)
		} else {
			if col.DatabaseTypeName() != "FLOAT8" || col.ScanType() != reflect.TypeFor[float64]() {
				t.Fatalf("FLOAT8 metadata=%q/%v", col.DatabaseTypeName(), col.ScanType())
			}
			if n, k := col.Nullable(); n || k {
				t.Fatalf("computed nullable=%v/%v", n, k)
			}
			if n, k := col.Length(); n != 0 || k {
				t.Fatalf("computed length=%d/%v", n, k)
			}
			if p, s, k := col.DecimalSize(); p != 0 || s != 0 || k {
				t.Fatalf("computed precision=%d/%d/%v", p, s, k)
			}
		}
	}
	if !rows.Next() {
		t.Fatal("missing NaN admission row", rows.Err())
	}
	values := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range dest {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, w := range want {
		if text, ok := w.(string); ok {
			if b.PG {
				if _, ok := values[i].([]byte); !ok {
					t.Fatalf("PG cube raw=%T", values[i])
				}
			} else {
				if _, ok := values[i].(string); !ok {
					t.Fatalf("native cube raw=%T", values[i])
				}
			}
			if fixture.Normalize(values[i]) != text {
				t.Errorf("cube=%v want %q", values[i], text)
			}
		} else {
			got, ok := values[i].(float64)
			if !ok {
				t.Fatalf("FLOAT8 raw=%T", values[i])
			}
			earthCoordinateFloat(t, got, w.(float64), 0)
		}
	}
	if rows.Next() {
		t.Fatal("extra NaN admission row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
