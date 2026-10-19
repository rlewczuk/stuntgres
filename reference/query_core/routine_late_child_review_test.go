package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func routineLateChildValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, typ string, value any) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func TestReferenceRoutineLateChildSelection(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_late_keep(integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1>0$$",
			"CREATE FUNCTION public.abs(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 3$$")
		queries := []string{
			"SELECT sum(1) FILTER (WHERE routine_late_keep(1)) AS v",
			"SELECT sum(1 ORDER BY routine_late_keep(1)) AS v",
			"SELECT pg_catalog.sum(1) FILTER (WHERE routine_late_keep(1)) AS v",
			"SELECT pg_catalog.sum(1 ORDER BY routine_late_keep(1)) AS v",
			"SELECT sum(1) FILTER (WHERE routine_late_keep(abs(true))) AS v",
			"SELECT sum(1 ORDER BY abs(true)) AS v",
			"SELECT sum(1) FILTER (WHERE routine_late_keep(abs(-1))) AS v",
			"SELECT sum(1 ORDER BY abs(-1)) AS v",
		}
		for _, q := range queries {
			routineLateChildValue(t, p, b, c, q, "INT8", int64(1))
		}
		routineExec(t, p, b, c,
			"CREATE FUNCTION public.sum(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 104$$")
		for _, q := range queries {
			routineLateChildValue(t, p, b, c, q, "INT8", int64(1))
		}
		routineLateChildValue(t, p, b, c, "SELECT sum(true) AS v", "INT4", int64(104))
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_late_body() RETURNS bigint LANGUAGE SQL AS $$SELECT sum(1 ORDER BY routine_late_keep(1)) FILTER (WHERE routine_late_keep(1))$$",
			"CREATE VIEW routine_late_view AS SELECT sum(1 ORDER BY routine_late_keep(1)) FILTER (WHERE routine_late_keep(1)) AS v")
		routineLateChildValue(t, p, b, c, "SELECT routine_late_body() AS v", "INT8", int64(1))
		routineLateChildValue(t, p, b, c, "SELECT v FROM routine_late_view", "INT8", int64(1))
		if b.PG {
			return
		}
		// Native controls are a bounded phase of the same SQL scenario.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		routineLateChildValue(t, p, b, c, "SELECT routine_late_body() AS v", "INT8", int64(1))
		routineLateChildValue(t, p, b, c, "SELECT v FROM routine_late_view", "INT8", int64(1))
	})
}

func TestReferenceRoutineLateChildUnwind(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_late_keep(integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1>0$$",
			"CREATE FUNCTION routine_late_bump(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION public.sum(boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 104$$",
			"CREATE SEQUENCE routine_late_once")
		q := "SELECT sum(1) FILTER (WHERE routine_late_keep(1)) AS v,routine_late_bump(nextval('routine_late_once')) AS n"
		n := int64(0)
		for _, tc := range []struct{ q, code, message, position, hint string }{
			{"SELECT sum(1) FILTER (WHERE routine_late_missing(1))", "42883", "function routine_late_missing(integer) does not exist", "29", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT sum(1 ORDER BY routine_late_missing(1))", "42883", "function routine_late_missing(integer) does not exist", "23", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT sum(1) FILTER (WHERE routine_late_bump(1))", "42804", "argument of FILTER must be type boolean, not type bigint", "29", ""},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Position, d.Hint = tc.position, tc.hint
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			for _, prepared := range []bool{false, true} {
				n++
				s = fixture.Select(q, []string{"v", "n"}, []string{"INT8", "INT8"}, [][]any{{int64(1), n}})
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		routineLateChildValue(t, p, b, c, "SELECT currval('routine_late_once') AS v", "INT8", n)
		for range 2 {
			n++
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
				fixture.Select(q, []string{"v", "n"}, []string{"INT8", "INT8"}, [][]any{{int64(1), n}}))
		}
		routineLateChildValue(t, p, b, c, "SELECT currval('routine_late_once') AS v", "INT8", n)
	})
}
