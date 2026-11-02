package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Settled by tests-1 on pinned PostgreSQL 18.6. Every unassigned portable
// diagnostic field is empty; the wrapper depends on first-earth provenance.
func earthQueryUnderflow(helper, box, wrapper string) fixture.Diagnostic {
	want := earthCoordinateBodyError(helper, "22003", "value out of range: underflow")
	if wrapper != "" {
		want.Context += "\nSQL function \"" + box + "\" " + wrapper
	}
	return want
}

func earthQueryLiteralPosition(q, literal string, want fixture.Diagnostic) fixture.Diagnostic {
	want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, literal)]) + 1)
	return want
}

func earthQueryAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tiny bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE earth_query_atomic(id integer,b cube)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_query_atomic VALUES (1,'(6378168)')", 1))
	for _, route := range []string{"direct", "fresh", "held"} {
		for _, parameter := range []bool{false, true} {
			q := "INSERT INTO earth_query_atomic VALUES (2,earth_box('(6378168)'::earth,0)),(3,earth_box('(0)'::earth,0)) RETURNING id"
			want := earthCheckDiagnostic("public", "on_surface", true)
			var args []any
			if parameter {
				q = "INSERT INTO earth_query_atomic VALUES (2,earth_box('(6378168)'::earth,0)),(3,earth_box($1::earth,0)) RETURNING id"
				args = []any{"(0)"}
				want = cubeParameterDiagnostic(want, "(0)", 1)
			}
			earthCoordinateError(t, p, b, c, q, route, args, want, false, nil)
			if tiny {
				q = "INSERT INTO earth_query_atomic VALUES (2,'(6378168)'),(3,earth_box('(6378168)'::earth,'5e-324'::float8)) RETURNING id"
				args = nil
				want = earthQueryUnderflow("gc_to_sec", "earth_box", "during inlining")
				if parameter {
					q = "INSERT INTO earth_query_atomic VALUES (2,'(6378168)'),(3,earth_box($1::earth,$2::float8)) RETURNING id"
					args = []any{"(6378168)", "5e-324"}
					want = earthQueryUnderflow("gc_to_sec", "earth_box", "statement 1")
				}
				earthCoordinateError(t, p, b, c, q, route, args, want, false, nil)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, cubeConstructorSelect(b, "SELECT id,b FROM earth_query_atomic ORDER BY id", []string{"id", "b"}, []string{"INT4", "CUBE"}, [][]any{{int64(1), "(6378168)"}}))
		}
	}
}

func TestReferenceEarthQueryDiagnostics(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ q, signature string }{
				{"SELECT earth_distance(NULL,NULL)", "earth_distance(unknown, unknown)"},
				{"SELECT earth_box(NULL,0)", "earth_box(unknown, integer)"},
			} {
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.q, route, nil, earthCoordinateMissing(tc.signature, "8"), true, nil)
				}
			}
		})
	})
	malformed := "SELECT 'é' AS marker,earth_box('(6378168,)'::earth,0)"
	overflow := "SELECT earth_box(NULL::earth,'1e999'::float8)"
	cases := []struct {
		name, literal, bound   string
		args, valid            []any
		literalWant, boundWant fixture.Diagnostic
		prepare                bool
	}{
		{"subnormal", "SELECT earth_box('(6378168)'::earth,'5e-324'::float8)", "SELECT earth_box($1::earth,$2::float8)", []any{"(6378168)", "5e-324"}, []any{"(6378168)", "0"}, earthQueryUnderflow("gc_to_sec", "earth_box", "during inlining"), earthQueryUnderflow("gc_to_sec", "earth_box", "statement 1"), false},
		{"null-subnormal", "SELECT earth_box(NULL::earth,'5e-324'::float8)", "SELECT earth_box('(6378168)'::earth,$1::float8)", []any{"5e-324"}, []any{"0"}, earthQueryUnderflow("gc_to_sec", "earth_box", "during inlining"), earthQueryUnderflow("gc_to_sec", "earth_box", "during inlining"), false},
		{"box-domain", "SELECT earth_box('(0)'::earth,0)", "SELECT earth_box($1::earth,0)", []any{"(0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "on_surface", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "(0)", 1), false},
		{"box-cube-domain", "SELECT earth_box('(0)'::cube,0)", "SELECT earth_box($1::earth,0)", []any{"(0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "on_surface", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "(0)", 1), false},
		{"distance-first-domain", "SELECT earth_distance('(0)'::earth,'(6378168)'::earth)", "SELECT earth_distance($1::earth,'(6378168)'::earth)", []any{"(0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "on_surface", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "(0)", 1), false},
		{"distance-second-domain", "SELECT earth_distance('(6378168)'::earth,'(0)'::earth)", "SELECT earth_distance('(6378168)'::earth,$1::earth)", []any{"(0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "on_surface", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "(0)", 1), false},
		{"not-point", "SELECT earth_box('(6378168,0),(6378168,1)'::earth,0)", "SELECT earth_box($1::earth,0)", []any{"(6378168,0),(6378168,1)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "not_point", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "not_point", true), "(6378168,0),(6378168,1)", 1), false},
		{"not-3d", "SELECT earth_distance('(6378168,0,0,0)'::earth,'(6378168)'::earth)", "SELECT earth_distance($1::earth,'(6378168)'::earth)", []any{"(6378168,0,0,0)"}, []any{"(6378168)"}, earthCheckDiagnostic("public", "not_3d", true), cubeParameterDiagnostic(earthCheckDiagnostic("public", "not_3d", true), "(6378168,0,0,0)", 1), false},
		{"malformed-multibyte", malformed, "SELECT 'é' AS marker,earth_box($1::earth,0)", []any{"(6378168,)"}, []any{"(6378168)"}, earthQueryLiteralPosition(malformed, "'(6378168,)'", cubeSyntaxDiagnostic(`syntax error at or near ")"`)), cubeParameterDiagnostic(cubeSyntaxDiagnostic(`syntax error at or near ")"`), "(6378168,)", 1), true},
		{"float-overflow", overflow, "SELECT earth_box(NULL::earth,$1::float8)", []any{"1e999"}, []any{"0"}, earthQueryLiteralPosition(overflow, "'1e999'", fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`)), cubeParameterDiagnostic(fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`), "1e999", 1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				earthInstall(t, p, b, c)
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.literal, route, nil, tc.literalWant, tc.prepare, nil)
					earthCoordinateError(t, p, b, c, tc.bound, route, tc.args, tc.boundWant, false, tc.valid)
				}
			})
		})
	}
	t.Run("signatures", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			for _, tc := range []struct{ q, signature string }{
				{"SELECT earth_distance(true,false)", "earth_distance(boolean, boolean)"},
				{"SELECT earth_box(true,0)", "earth_box(boolean, integer)"},
				{"SELECT earth_box(NULL::earth,true)", "earth_box(earth, boolean)"},
				{"SELECT earth_box(NULL::earth)", "earth_box(earth)"},
				{"SELECT earth_distance(NULL::earth)", "earth_distance(earth)"},
				{"SELECT earth_box(DISTINCT true)", "earth_box(boolean)"},
				{"SELECT pg_catalog.earth_box(NULL::earth,0)", "pg_catalog.earth_box(earth, integer)"},
			} {
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.q, route, nil, earthCoordinateMissing(tc.signature, "8"), true, nil)
				}
			}
		})
	})
	t.Run("atomicity", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			earthQueryAtomicity(t, p, b, c, true)
		})
	})
	t.Run("cancellation", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "SELECT earth_box('(6378168)'::earth,0)")
			if rows != nil {
				rows.Close()
			}
			// Native admission rejects a pre-canceled context ordinarily.
			// A fast pq command may instead finish before observing cancellation.
			if !b.PG && !errors.Is(err, context.Canceled) {
				t.Fatal("native pre-canceled public query", err)
			}
			// Return the pq borrower before checking health through the pool.
			c.Close()
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT earth_distance('(6378168)'::earth,'(6378168)'::earth) AS d", []string{"d"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		})
	})
}
