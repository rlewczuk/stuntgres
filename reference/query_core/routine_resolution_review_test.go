package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The initial winners and ambiguity diagnostics were observed by the root
// against pinned PostgreSQL 18.6 in tests-46.md. The builtin/SQL concat ambiguity
// below is an additional independent expectation requiring fresh oracle proof.

func TestReferenceRoutineResolutionProviderCoexistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "routine_review_early", "routine_review_late")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.array_append(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION public.concat(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+100$$",
			"CREATE FUNCTION public.parse_ident(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+200$$",
			"CREATE SCHEMA routine_review_early", "CREATE SCHEMA routine_review_late",
			"CREATE FUNCTION routine_review_early.concat(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 301$$",
			"CREATE FUNCTION routine_review_late.concat(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 302$$",
			"CREATE FUNCTION routine_review_late.concat(text,text) RETURNS integer LANGUAGE SQL AS $$SELECT 303$$")
		for _, tc := range []struct {
			q, typ string
			value  any
		}{
			{"SELECT array_append(ARRAY[1],2)::text AS v", "TEXT", "{1,2}"},
			{"SELECT array_append(3,4) AS v", "INT4", int64(7)},
			{"SELECT concat('a','b') AS v", "TEXT", "ab"},
			{"SELECT concat(4) AS v", "INT4", int64(104)},
			{"SELECT parse_ident('Foo.Bar')::text AS v", "TEXT", "{foo,bar}"},
			{"SELECT parse_ident(4) AS v", "INT4", int64(204)},
			{"SELECT pg_catalog.concat(4) AS v", "TEXT", "4"},
			{"SELECT pg_catalog.array_append(ARRAY[1],2)::text AS v", "TEXT", "{1,2}"},
		} {
			s := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			q, typ string
			value  any
			arg    any
		}{
			{"SELECT array_append(ARRAY[1],$1)::text AS v", "TEXT", "{1,2}", int64(2)},
			{"SELECT concat($1::integer) AS v", "INT4", int64(104), int64(4)},
			{"SELECT parse_ident($1::text)::text AS v", "TEXT", "{foo,bar}", "Foo.Bar"},
		} {
			s := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, tc.arg)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		routineExec(t, p, b, c, "SET search_path TO routine_review_early,pg_catalog,routine_review_late,public")
		routineValue(t, p, b, c, "SELECT concat(4) AS v", "v", "INT4", int64(301))
		// A later exact concrete signature beats the variadic any provider.
		routineValue(t, p, b, c, "SELECT concat('a'::text,'b'::text) AS v", "v", "INT4", int64(303))
		// The same input signature is masked, not every later same-name routine.
		routineExec(t, p, b, c, "SET search_path TO routine_review_late,routine_review_early,pg_catalog,public")
		routineValue(t, p, b, c, "SELECT concat(4) AS v", "v", "INT4", int64(302))
	})
}

func TestReferenceRoutineResolutionUnknownPositions(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.concat(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION public.concat(boolean,boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_conflict(integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_conflict(boolean,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_reverse(text,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_reverse(integer,boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_cross(text,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_cross(integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_string(integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_string(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_preferred(double precision,text) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_preferred(numeric,text) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_textpreferred(text,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_textpreferred(varchar,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_known(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_known(integer,boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$",
			"CREATE FUNCTION routine_review_known_reverse(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE FUNCTION routine_review_known_reverse(boolean,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$")
		for _, tc := range []struct{ q, message string }{
			{"SELECT routine_review_conflict(NULL,'x')", "function routine_review_conflict(unknown, unknown) is not unique"},
			{"SELECT routine_review_reverse('x',NULL)", "function routine_review_reverse(unknown, unknown) is not unique"},
			{"SELECT routine_review_cross(NULL,NULL)", "function routine_review_cross(unknown, unknown) is not unique"},
			// The executable ANY provider must not hide genuine SQL ambiguity.
			{"SELECT concat(NULL,NULL)", "function concat(unknown, unknown) is not unique"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42725", tc.message)
			d.Position = "8"
			d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, q := range []string{
			"SELECT routine_review_string(NULL,'7') AS v",
			"SELECT routine_review_string(1,'7') AS v",
			"SELECT routine_review_preferred('7',NULL) AS v",
			"SELECT routine_review_textpreferred('x',1) AS v",
			"SELECT routine_review_known(1,NULL) AS v",
			"SELECT routine_review_known_reverse(NULL,1) AS v",
		} {
			routineValue(t, p, b, c, q, "v", "INT4", int64(1))
		}
	})
}

func TestReferenceRoutineResolutionOriginalRecipes(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.num_nulls(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 99$$",
			"CREATE SEQUENCE routine_review_once")
		routineValue(t, p, b, c, "SELECT num_nulls(true) AS v", "v", "INT4", int64(99))
		routineValue(t, p, b, c, "SELECT num_nulls(+1,NULL) AS v", "v", "INT4", int64(1))
		q := "SELECT num_nulls(nextval('routine_review_once'),NULL) AS v"
		s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		routineValue(t, p, b, c, "SELECT currval('routine_review_once') AS v", "v", "INT8", int64(1))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// Preparation and repeated current-value probes must not run actuals.
		routineValue(t, p, b, c, "SELECT currval('routine_review_once') AS v", "v", "INT8", int64(1))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		routineValue(t, p, b, c, "SELECT currval('routine_review_once') AS v", "v", "INT8", int64(2))
		q = "SELECT num_nulls(CAST(1+2 AS int4),NULL) AS v"
		if b.PG {
			s = fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		} else {
			// This is an existing explicit native boundary, not an overload-
			// resolution failure. A private bound-argument carrier cannot hide it.
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: "24"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		routineValue(t, p, b, c, "SELECT currval('routine_review_once') AS v", "v", "INT8", int64(2))
	})
}
