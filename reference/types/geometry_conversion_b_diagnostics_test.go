package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionBDiagnostics(t *testing.T) {
	geometryConversionBScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, signature string }{
			{"SELECT lseg()", "lseg()"},
			{"SELECT lseg(1)", "lseg(integer)"},
			{"SELECT lseg(point '(0,0)')", "lseg(point)"},
			{"SELECT lseg(point '(0,0)',1)", "lseg(point, integer)"},
			{"SELECT lseg(point '(0,0)',point '(1,1)',point '(2,2)')", "lseg(point, point, point)"},
			{"SELECT path()", "path()"},
			{"SELECT path(point '(0,0)')", "path(point)"},
			{"SELECT path(NULL::polygon,NULL::polygon)", "path(polygon, polygon)"},
			{"SELECT point()", "point()"},
			{"SELECT point(1)", "point(integer)"},
			{"SELECT point(true,1)", "point(boolean, integer)"},
			{"SELECT point(1,2,3)", "point(integer, integer, integer)"},
			{"SELECT point(NULL::path)", "point(path)"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(tc.q, "42883", "function "+tc.signature+" does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
				})
			}
		}
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT point('bad',2)", "22P02", "invalid input syntax for type double precision: \"bad\"", "14", "", prepared, false))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT point($1) AS v", "42725", "function point(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts.", prepared, false, nil))
		}
		for _, tc := range geometryConversionBCases {
			t.Run(tc.name+"/codec", func(t *testing.T) {
				q := "SELECT " + tc.parameter + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				valid := geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
				for i, formal := range tc.formals {
					if formal == "float8" {
						formal = "double precision"
					}
					for _, bad := range []any{"bad", []byte{}} {
						text := "bad"
						if _, ok := bad.([]byte); ok {
							text = ""
						}
						args := append([]any(nil), tc.args...)
						args[i] = bad
						d := fixture.ControlsReferenceDiagnostic("22P02", fmt.Sprintf("invalid input syntax for type %s: %q", formal, text))
						if text == "" {
							d.Context = fmt.Sprintf("unnamed portal parameter $%d = ''", i+1)
						} else {
							d.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", i+1)
						}
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.SelectRows = true
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						args[i] = nil
						fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{nil}}, args...))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
					}
				}
			})
		}
		for _, tc := range []struct{ formal, input, failure string }{
			{"box", "(1e308,0),(1e308,0)", "overflow"},
			{"lseg", "[(1e308,0),(1e308,0)]", "overflow"},
			{"box", "(5e-324,0),(0,0)", "underflow"},
			{"lseg", "[(5e-324,0),(0,0)]", "underflow"},
			{"polygon", "((1e308,0),(1e308,0))", "overflow"},
			{"polygon", "((0,0),(5e-324,0))", "underflow"},
			{"polygon", "((1e308,0),(-1e308,0))", "overflow"},
		} {
			t.Run(tc.formal+"/"+tc.input, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT point("+tc.formal+" '"+tc.input+"')", "22003", "value out of range: "+tc.failure, "", "", prepared, true))
				}
				q := "SELECT point($1::" + tc.formal + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := boxError(q, "22003", "value out of range: "+tc.failure, "", "", false, true, tc.input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				good := "((2,3))"
				if tc.formal == "box" {
					good = "(2,3),(2,3)"
				}
				if tc.formal == "lseg" {
					good = "[(2,3),(2,3)]"
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionSelect(b, q, []string{"v"}, []string{"POINT"}, [][]any{{"(2,3)"}}, good))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT circle(polygon '((1e308,0),(-1e308,0))')", "22003", "value out of range: overflow", "", "", false, true))
		geometryConversionBAtomicity(t, p, b, c)
	})
}

func geometryConversionBAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE geom_b_atomic(id integer NOT NULL,v point)", 0))
	run(fixture.Exec("INSERT INTO geom_b_atomic VALUES(1,point(1,2))", 1))
	d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
	run(fixture.ErrorDiagnostic("INSERT INTO geom_b_atomic VALUES(2,point(polygon '((2,3))')),(3,point(polygon '((1e308,0),(-1e308,0))'))", d))
	baseline := geometryConversionSelect(b, "SELECT id,v FROM geom_b_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "POINT"}, [][]any{{int64(1), "(1,2)"}})
	run(baseline)
	q := "INSERT INTO geom_b_atomic VALUES($1,point($2::polygon))"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, 2, "((1e308,0),(-1e308,0))"))
	run(baseline)
	if !b.PG {
		// Precanceled ordinary context errors are a native contract; pq can
		// nondeterministically discard the transport before server admission.
		cancelled, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, exec := range []interface {
			ExecContext(context.Context, string, ...any) (sql.Result, error)
		}{c, held} {
			_, err := exec.ExecContext(cancelled, q, 9, "((2,3))")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled insert: %v", err)
			}
		}
		run(baseline)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "((2,3))"))
	run(geometryConversionSelect(b, "SELECT id,v FROM geom_b_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "POINT"}, [][]any{{int64(1), "(1,2)"}, {int64(2), "(2,3)"}}))
}
