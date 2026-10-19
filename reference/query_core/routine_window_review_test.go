package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Window and SQL scalar expressions have different native scan/nullability
// contracts. Assert their public names, declared database types and cells using
// the ordinary fixture instead of imposing the SQL-scalar-only metadata helper.
func routineWindowReviewValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name, typ string, value any) {
	t.Helper()
	s := fixture.Select(q, []string{name}, []string{typ}, [][]any{{value}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func TestReferenceRoutineWindowCoexistence(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineWindowReviewValue(t, p, b, c, "SELECT row_number() OVER () AS v", "v", "INT8", int64(1))
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.row_number(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+100$$")
		routineWindowReviewValue(t, p, b, c, "SELECT row_number() OVER () AS v", "v", "INT8", int64(1))
		routineValue(t, p, b, c, "SELECT row_number(4) AS v", "v", "INT4", int64(104))
		routineValue(t, p, b, c, "SELECT row_number((row_number() OVER ())::integer) AS v", "v", "INT4", int64(101))
		routineWindowReviewValue(t, p, b, c, "SELECT pg_catalog.row_number() OVER () AS v", "v", "INT8", int64(1))
		for _, path := range []string{"public,pg_catalog", "pg_catalog,public"} {
			routineExec(t, p, b, c, "SET search_path TO "+path)
			routineWindowReviewValue(t, p, b, c, "SELECT row_number() OVER () AS v", "v", "INT8", int64(1))
			routineValue(t, p, b, c, "SELECT row_number(4) AS v", "v", "INT4", int64(104))
		}
		// Exact input signatures, not the whole name, are masked by the path.
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.row_number() RETURNS integer LANGUAGE SQL AS $$SELECT 77$$",
			"SET search_path TO public,pg_catalog")
		routineValue(t, p, b, c, "SELECT row_number() AS v", "v", "INT4", int64(77))
		routineWindowReviewValue(t, p, b, c, "SELECT pg_catalog.row_number() OVER () AS v", "v", "INT8", int64(1))
		routineExec(t, p, b, c, "SET search_path TO pg_catalog,public")
		routineWindowReviewValue(t, p, b, c, "SELECT row_number() OVER () AS v", "v", "INT8", int64(1))
	})
}

func TestReferenceRoutineWindowPolymorphic(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineWindowReviewValue(t, p, b, c, "SELECT first_value(7) OVER () AS v", "v", "INT4", int64(7))
		routineWindowReviewValue(t, p, b, c, "SELECT lag(7,1,9) OVER () AS v", "v", "INT4", int64(9))
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.first_value(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 104$$",
			"CREATE FUNCTION public.lag(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 105$$")
		for _, tc := range []struct {
			q     string
			value any
		}{
			{"SELECT first_value(7) OVER () AS v", int64(7)},
			{"SELECT lag(7,1,9) OVER () AS v", int64(9)},
			{"SELECT lag(7) OVER () AS v", nil},
			{"SELECT first_value(true) AS v", int64(104)},
			{"SELECT lag(true) AS v", int64(105)},
		} {
			routineWindowReviewValue(t, p, b, c, tc.q, "v", "INT4", tc.value)
		}
		routineWindowReviewValue(t, p, b, c, "SELECT first_value((SELECT row_number() OVER ())) OVER () AS v", "v", "INT8", int64(1))
		q := "SELECT first_value($1::integer) OVER () AS v"
		s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}, int64(8))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}

func TestReferenceRoutineWindowDiagnostics(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.row_number(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+100$$",
			"CREATE FUNCTION public.first_value(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 104$$",
			"CREATE FUNCTION routine_window_identity(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$")
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT row_number(4) OVER ()", "42809", "OVER specified, but row_number is not a window function nor an aggregate function", "8"},
			{"SELECT public.row_number(4) OVER ()", "42809", "OVER specified, but public.row_number is not a window function nor an aggregate function", "8"},
			{"SELECT first_value(true) OVER ()", "42809", "OVER specified, but first_value is not a window function nor an aggregate function", "8"},
			{"SELECT row_number()", "42809", "window function row_number requires an OVER clause", "8"},
			{"SELECT first_value(row_number() OVER ()) OVER ()", "42P20", "window function calls cannot be nested", "20"},
			{"SELECT first_value(routine_window_identity(row_number() OVER ())) OVER ()", "42P20", "window function calls cannot be nested", "44"},
			{"SELECT 1 WHERE row_number() OVER ()>0", "42P20", "window functions are not allowed in WHERE", "16"},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Position = tc.position
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
}
