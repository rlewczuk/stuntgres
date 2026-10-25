package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionCDiagnostics(t *testing.T) {
	geometryConversionCScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, code, message string }{
			{"polygon(path '[(0,0),(1,1),(0,0)]')", "22023", "open path cannot be converted to polygon"},
			{"polygon(2147483647,circle '<(0,0),1>')", "54000", "too many points requested"},
			{"polygon(2,circle(point '(1e308,0)',1e308))", "22003", "value out of range: overflow"},
			{"polygon(box 'bad')", "22P02", "invalid input syntax for type box: \"bad\""},
			{"polygon(circle 'bad')", "22P02", "invalid input syntax for type circle: \"bad\""},
			{"polygon(path 'bad')", "22P02", "invalid input syntax for type path: \"bad\""},
			{"polygon(0,circle 'bad')", "22P02", "invalid input syntax for type circle: \"bad\""},
			{"polygon(0,circle '<(0,0),-1>')", "22P02", "invalid input syntax for type circle: \"<(0,0),-1>\""},
			{"polygon(0,circle '<(1e400,0),1>')", "22003", "\"1e400\" is out of range for type double precision"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					position := ""
					runtime := tc.code != "22P02" && tc.message != "\"1e400\" is out of range for type double precision"
					if !runtime {
						switch tc.expr {
						case "polygon(box 'bad')":
							position = "20"
						case "polygon(circle 'bad')":
							position = "23"
						case "polygon(path 'bad')":
							position = "21"
						default:
							position = "25"
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expr, tc.code, tc.message, position, "", prepared, runtime))
				}
			})
		}
		for _, radius := range []string{"0", "-0", "1e-6", "-1e-6", "5e-7", "-5e-7", "1", "1.0000000000000002e-6", "NaN", "Infinity", "-Infinity"} {
			for _, n := range []int{-1, 0, 1, 2147483647} {
				t.Run(fmt.Sprintf("n%d/r%s", n, radius), func(t *testing.T) {
					code, message := "22023", "must request at least 2 points"
					if radius == "0" || radius == "-0" || radius == "1e-6" || radius == "-1e-6" || radius == "5e-7" || radius == "-5e-7" {
						code, message = "0A000", "cannot convert circle with radius zero to polygon"
					} else if n == 2147483647 {
						code, message = "54000", "too many points requested"
					}
					q := fmt.Sprintf("SELECT polygon(%d,circle(point '(0,0)','%s'::float8))", n, radius)
					for _, prepared := range []bool{false, true} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, code, message, "", "", prepared, true))
					}
				})
			}
		}
		for _, radius := range []string{"0", "-0", "1e-6", "-1e-6"} {
			t.Run("unary/"+radius, func(t *testing.T) {
				q := "SELECT polygon(circle(point '(0,0)'," + radius + "))"
				for _, prepared := range []bool{false, true} {
					s := boxError(q, "0A000", "cannot convert circle with radius zero to polygon", "", "", prepared, true)
					s.Diagnostic.Context = "SQL function \"polygon\" statement 1"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, tc := range geometryConversionCCases {
			t.Run(tc.name+"/bound_codec", func(t *testing.T) {
				q := "SELECT " + tc.parameter + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				valid := geometryConversionCSelect(b, q, tc.want, tc.args...)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
				for i, formal := range tc.formals {
					for _, bad := range []string{"bad", ""} {
						args := append([]any(nil), tc.args...)
						args[i] = bad
						d := fixture.ControlsReferenceDiagnostic("22P02", fmt.Sprintf("invalid input syntax for type %s: %q", formal, bad))
						d.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", i+1)
						if bad == "" {
							d.Context = fmt.Sprintf("unnamed portal parameter $%d = ''", i+1)
						}
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						args[i] = nil
						fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionCSelect(b, q, nil, args...))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
					}
				}
			})
		}
		geometryConversionCAtomicity(t, p, b, c)
	})
}

func geometryConversionCAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE geom_c_atomic(id integer NOT NULL,v polygon)", 0))
	run(fixture.Exec("INSERT INTO geom_c_atomic VALUES(1,polygon(path '((2,3))'))", 1))
	d := fixture.ControlsReferenceDiagnostic("22023", "open path cannot be converted to polygon")
	run(fixture.ErrorDiagnostic("INSERT INTO geom_c_atomic VALUES(2,polygon(path '((4,5))')),(3,polygon(path '[(0,0),(1,1),(0,0)]'))", d))
	baseline := geometryConversionSelect(b, "SELECT id,v FROM geom_c_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(1), "((2,3))"}})
	run(baseline)
	q := "INSERT INTO geom_c_atomic VALUES($1,polygon($2::path))"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, 2, "[(0,0),(1,1),(0,0)]"))
	run(baseline)
	if !b.PG {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		_, err := held.ExecContext(ctx, q, 9, "((2,3))")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
		run(baseline)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "((4,5))"))
	run(geometryConversionSelect(b, "SELECT id,v FROM geom_c_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(1), "((2,3))"}, {int64(2), "((4,5))"}}))
}
