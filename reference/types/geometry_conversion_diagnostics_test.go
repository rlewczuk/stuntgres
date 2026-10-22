package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionDiagnostics(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("utf8_location/prepared=%t", prepared), func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT 'é' AS prefix,box(1)", "42883", "function box(integer) does not exist", "22", hint, prepared, false))
			})
		}
		for _, tc := range []struct{ q, signature string }{
			{"SELECT box(1)", "box(integer)"},
			{"SELECT box()", "box()"},
			{"SELECT box(point '(0,0)',point '(1,1)',point '(2,2)')", "box(point, point, point)"},
			{"SELECT bound_box(box '(1,1),(0,0)')", "bound_box(box)"},
			{"SELECT circle(point '(0,0)',true)", "circle(point, boolean)"},
			{"SELECT circle(point '(0,0)',1,2)", "circle(point, integer, integer)"},
			{"SELECT line(point '(0,0)')", "line(point)"},
			{"SELECT line(point '(0,0)',1)", "line(point, integer)"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(tc.q, "42883", "function "+tc.signature+" does not exist", "8", hint, prepared, false))
				})
			}
		}
		for _, name := range []string{"box", "circle"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("ambiguous_%s/prepared=%t", name, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+name+"($1) AS v", "42725", "function "+name+"(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts.", prepared, false, nil))
				})
			}
		}
		for _, tc := range []struct {
			q, code, message, position string
			runtime                    bool
			args                       []any
		}{
			{"SELECT '<(0,0),-1>'::circle", "22P02", "invalid input syntax for type circle: \"<(0,0),-1>\"", "8", false, nil},
			{"SELECT circle(point '(0,0)','bad')", "22P02", "invalid input syntax for type double precision: \"bad\"", "29", false, nil},
			{"SELECT line(point '(1,1)',point '(1,1)')", "22023", "invalid line specification: must be two distinct points", "", true, nil},
			{"SELECT line(point '(0,0)',point '(0.000001,0.000001)')", "22023", "invalid line specification: must be two distinct points", "", true, nil},
			{"SELECT line(point '(NaN,2)',point '(NaN,2)')", "22023", "invalid line specification: must be two distinct points", "", true, nil},
			{"SELECT line($1::point,$2::point)", "22023", "invalid line specification: must be two distinct points", "", true, []any{"(1,1)", "(1,1)"}},
			{"SELECT circle(box '(1e308,1e308),(1e308,1e308)')", "22003", "value out of range: overflow", "", true, nil},
			{"SELECT line(point '(1e308,1e308)',point '(-1e308,-1e308)')", "22003", "value out of range: overflow", "", true, nil},
			{"SELECT box(circle(point '(1.7e308,0)',1e308))", "22003", "value out of range: overflow", "", true, nil},
			{"SELECT circle(polygon '((0,0),(5e-324,0))')", "22003", "value out of range: underflow", "", true, nil},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(tc.q, tc.code, tc.message, tc.position, "", prepared, tc.runtime, tc.args...))
				})
			}
		}
		q := "SELECT circle($1::point,$2::float8) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type double precision: \"bad\"")
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, "(0,0)", "bad")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionSelect(b, q, []string{"v"}, []string{"CIRCLE"}, [][]any{{"<(0,0),-1>"}}, "(0,0)", -1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionSelect(b, q, []string{"v"}, []string{"CIRCLE"}, [][]any{{nil}}, nil, -1))
		t.Run("negative_binary_input_still_rejected", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE geom_conversion_binary(c circle)", 0))
			pool := p.Native
			if b.PG {
				pool = p.CopyPostgres
			}
			stream, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			negative := make([]byte, 24)
			negative[16], negative[17] = 0xbf, 0xf0 // IEEE754 -1; center (0,0).
			result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY geom_conversion_binary FROM STDIN WITH(FORMAT binary)", bytes.NewReader(circleBinaryFrame(negative)))
			want := fixture.ControlsReferenceDiagnostic("22P03", `invalid radius in external "circle" value`)
			want.Context = "COPY geom_conversion_binary, line 1, column c"
			fixture.AssertDiagnostic(t, err, &want)
			if result.Count != 0 || result.CommandTag != "" {
				t.Fatalf("failed COPY published %+v", result)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM geom_conversion_binary", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}

func geometryConversionAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE geom_conversion_atomic(id integer,l line)", 0))
	q := "INSERT INTO geom_conversion_atomic VALUES($1,line($2::point,$3::point))"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	d := fixture.ControlsReferenceDiagnostic("22023", "invalid line specification: must be two distinct points")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, 1, "(1,1)", "(1,1)"))
	run(fixture.Select("SELECT count(*) AS n FROM geom_conversion_atomic", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1, "(2,1)", "(2,9)"))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, nil, "(2,9)"))
	run(fixture.ErrorDiagnostic("INSERT INTO geom_conversion_atomic VALUES(3,line(point '(0,0)',point '(1,1)')),(4,line(point '(1,1)',point '(1,1)'))", d))
	run(geometryConversionSelect(b, "SELECT id,l FROM geom_conversion_atomic ORDER BY id", []string{"id", "l"}, []string{"INT4", "LINE"}, [][]any{{int64(1), "{-1,0,2}"}, {int64(2), nil}}))
}

func TestReferenceGeometryConversionFeatureSmoke(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		s := geometryConversionSelect(b, "SELECT box('<(0,0),2>'::circle) AS b,circle(box '(6,8),(0,0)') AS c,line(point '(1,3)',point '(3,7)') AS l,circle(point '(0,0)',-1) AS negative,radius(circle(point '(0,0)',-1)) AS r", []string{"b", "c", "l", "negative", "r"}, []string{"BOX", "CIRCLE", "LINE", "CIRCLE", "FLOAT8"}, [][]any{{geometryConversionCases[0].want, "<(3,4),5>", "{2,-1,1}", "<(0,0),-1>", float64(-1)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		geometryConversionAtomicity(t, p, b, c)
	})
}
