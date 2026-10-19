package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineCoreCallableKinds(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "routine_kind")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA routine_kind",
			"CREATE TABLE routine_kind.rows(x integer)",
			"CREATE FUNCTION routine_kind.sum(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+100$$",
			"CREATE FUNCTION routine_kind.count(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+200$$",
			"CREATE FUNCTION routine_kind.int4(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+300$$",
			"CREATE FUNCTION routine_kind.int4(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT CASE WHEN $1 THEN 304 ELSE 305 END$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_kind.rows VALUES(1),(2)", 2))
		checkRows := func(q, typ string, rows [][]any) {
			s := fixture.Select(q, []string{"v"}, []string{typ}, rows)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		checkRows("SELECT routine_kind.sum(x) AS v FROM routine_kind.rows ORDER BY x", "INT4", [][]any{{int64(101)}, {int64(102)}})
		checkRows("SELECT routine_kind.count(x) AS v FROM routine_kind.rows ORDER BY x", "INT4", [][]any{{int64(201)}, {int64(202)}})
		routineExec(t, p, b, c, "SET search_path TO routine_kind,public")
		checkRows("SELECT sum(x) AS v FROM routine_kind.rows", "INT8", [][]any{{int64(3)}})
		routineExec(t, p, b, c, "SET search_path TO routine_kind,pg_catalog,public")
		checkRows("SELECT sum(x) AS v FROM routine_kind.rows ORDER BY x", "INT4", [][]any{{int64(101)}, {int64(102)}})
		checkRows("SELECT count(x) AS v FROM routine_kind.rows ORDER BY x", "INT4", [][]any{{int64(201)}, {int64(202)}})
		checkRows("SELECT routine_kind.sum(ALL x) AS v FROM routine_kind.rows ORDER BY x", "INT4", [][]any{{int64(101)}, {int64(102)}})
		checkRows("SELECT sum(x::bigint) AS v FROM routine_kind.rows", "NUMERIC", [][]any{{"3"}})
		routineValue(t, p, b, c, "SELECT routine_kind.int4(4) AS v", "v", "INT4", int64(304))
		routineValue(t, p, b, c, "SELECT int4(4) AS v", "v", "INT4", int64(304))
		routineValue(t, p, b, c, "SELECT int4(1=1) AS v", "v", "INT4", int64(304))
		// A merely coercible SQL candidate must not steal the type-name cast.
		routineValue(t, p, b, c, "SELECT int4('4') AS v", "v", "INT4", int64(4))
		// Complete records/stages observed on pinned18.6 in tests-6.md.
		for _, tc := range []struct {
			q, code, message, hint string
			args                   []any
		}{
			{"SELECT int4($1) AS v", "42725", "function int4(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", []any{4}},
			{"SELECT routine_kind.sum(DISTINCT x) FROM routine_kind.rows", "42809", "DISTINCT specified, but routine_kind.sum is not an aggregate function", "", nil},
			{"SELECT routine_kind.sum(x) FILTER (WHERE true) FROM routine_kind.rows", "42809", "FILTER specified, but routine_kind.sum is not an aggregate function", "", nil},
		} {
			want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			want.Position = "8"
			want.Hint = tc.hint
			s := fixture.ErrorDiagnostic(tc.q, want, tc.args...)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
