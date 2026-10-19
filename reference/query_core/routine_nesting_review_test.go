package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineNestingDiagnostics(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_nesting_identity(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$")
		for _, installed := range []bool{false, true} {
			if installed {
				routineExec(t, p, b, c,
					"CREATE FUNCTION public.sum(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 104$$",
					"CREATE FUNCTION public.count(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 105$$")
			}
			for _, name := range []string{"sum", "count", "pg_catalog.sum", "pg_catalog.count"} {
				for _, operand := range []string{"row_number() OVER ()", "routine_nesting_identity(row_number() OVER ())"} {
					for _, over := range []bool{false, true} {
						q := "SELECT " + name + "(" + operand + ")"
						code, message := "42803", "aggregate function calls cannot contain window function calls"
						if over {
							q += " OVER ()"
							code, message = "42P20", "window function calls cannot be nested"
						}
						d := fixture.ControlsReferenceDiagnostic(code, message)
						d.Position = fixture.Position(strings.Index(q, "row_number") + 1)
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						s.ErrorStage = "prepare"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					}
				}
			}
		}
	})
}

func TestReferenceRoutineNestingBoundaries(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_nesting_identity(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION routine_nesting_body() RETURNS bigint LANGUAGE SQL AS $$SELECT row_number() OVER ()$$")
		for _, installed := range []bool{false, true} {
			if installed {
				routineExec(t, p, b, c,
					"CREATE FUNCTION public.sum(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 104$$",
					"CREATE FUNCTION public.count(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 105$$")
			}
			for _, name := range []string{"sum", "count", "pg_catalog.sum", "pg_catalog.count"} {
				for _, operand := range []string{"(SELECT row_number() OVER ())", "routine_nesting_body()"} {
					for _, suffix := range []string{"", " OVER ()"} {
						typ, value := "INT8", any(int64(1))
						if strings.HasSuffix(name, "sum") {
							typ, value = "NUMERIC", "1"
						}
						q := "SELECT " + name + "(" + operand + ")" + suffix + " AS v"
						if !b.PG && strings.HasSuffix(name, "count") && suffix != "" {
							// The incumbent executor supports COUNT(*) OVER, not
							// COUNT(expr) OVER. Legal child-frame operands must
							// reach that existing boundary, never a nesting error.
							s := fixture.Error(q, "0A000")
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							s.ErrorStage = "prepare"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
						} else {
							routineWindowReviewValue(t, p, b, c, q, "v", typ, value)
						}
					}
				}
			}
			routineWindowReviewValue(t, p, b, c, "SELECT routine_nesting_identity(row_number() OVER ()) AS v", "v", "INT8", int64(1))
		}
		// A genuine SQL scalar winner may consume a window result, even when
		// its name also has incumbent aggregate signatures.
		routineExec(t, p, b, c, "SET search_path TO public,pg_catalog",
			"CREATE FUNCTION public.sum(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+10$$",
			"CREATE FUNCTION public.count(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+20$$")
		routineWindowReviewValue(t, p, b, c, "SELECT sum(row_number() OVER ()) AS v", "v", "INT8", int64(11))
		routineWindowReviewValue(t, p, b, c, "SELECT count(row_number() OVER ()) AS v", "v", "INT8", int64(21))
	})
}
