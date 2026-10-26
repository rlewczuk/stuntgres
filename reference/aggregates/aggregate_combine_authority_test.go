package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateCombineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ac_owner", "ac_reader", "ac_string_owner", "ac_string_reader"})
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
		for _, q := range []string{
			"CREATE ROLE ac_owner", "CREATE ROLE ac_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO ac_owner", "GRANT USAGE ON SCHEMA public TO ac_reader",
			"CREATE FUNCTION ac_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+$2::bigint*$2::bigint$$",
			"CREATE FUNCTION ac_merge(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$",
			"REVOKE EXECUTE ON FUNCTION ac_merge(bigint,bigint) FROM PUBLIC",
			"CREATE TABLE ac_rights_input(x integer)",
			"GRANT SELECT ON ac_rights_input TO ac_reader",
			"SET SESSION AUTHORIZATION ac_owner",
		} {
			exec(q)
		}
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		const ddl = "CREATE AGGREGATE ac_owned(integer) (SFUNC=ac_step,STYPE=bigint,COMBINEFUNC=ac_merge,PARALLEL=SAFE)"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(ddl, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ac_merge"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ac_merge(bigint,bigint) TO ac_owner", "SET SESSION AUTHORIZATION ac_owner", ddl, "REVOKE EXECUTE ON FUNCTION ac_owned(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ac_owned(integer) TO ac_reader", "RESET SESSION AUTHORIZATION"} {
			exec(q)
		}
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ac_rights_input VALUES(1),(2),(3),(4)", 4))
		exec("SET SESSION AUTHORIZATION ac_reader")
		const q = "SELECT ac_owned(x) AS v FROM ac_rights_input WHERE x<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(30), 4))
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ac_merge(bigint,bigint) FROM ac_owner", "SET SESSION AUTHORIZATION ac_reader", "SET max_parallel_workers_per_gather=0"} {
			exec(sql)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, nil, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(30), 4))
		const limitQuery = "SELECT ac_owned(x) AS v FROM ac_rights_input LIMIT 0"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(limitQuery, []string{"v"}, []string{"INT8"}, nil))
		// The serial stage uses SFUNC, not COMBINEFUNC. Revoke its owner's
		// support right to exercise the reachable PostgreSQL42501 boundary.
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ac_step(bigint,integer) FROM PUBLIC", "SET SESSION AUTHORIZATION ac_reader"} {
			exec(sql)
		}
		for _, limit := range []int{0, 4} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ac_step"}, limit)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		limitZero := fixture.ImplicitError(limitQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ac_step"})
		limitZero.SelectRows, limitZero.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, limitZero)
		exec("RESET SESSION AUTHORIZATION")
		exec("GRANT EXECUTE ON FUNCTION ac_step(bigint,integer) TO PUBLIC")
		exec("SET SESSION AUTHORIZATION ac_reader")
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ac_merge(bigint,bigint) TO ac_owner", "SET SESSION AUTHORIZATION ac_reader"} {
			exec(sql)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, int64(30), 4))
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ac_owned(integer) FROM ac_reader", "GRANT EXECUTE ON FUNCTION ac_merge(bigint,bigint) TO ac_reader", "SET SESSION AUTHORIZATION ac_reader"} {
			exec(sql)
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate ac_owned"}, 0)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		exec("RESET SESSION AUTHORIZATION")
	})
	t.Run("builtin_string_support", aggregateCombineStringAuthority)
}

func TestReferenceAggregateCombineDependencies(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION ac_dep_merge(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$", 0),
			fixture.Exec("CREATE FUNCTION ac_dep_gt(integer,integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1>$2$$", 0),
			fixture.Exec("CREATE OPERATOR public.># (LEFTARG=integer,RIGHTARG=integer,FUNCTION=ac_dep_gt)", 0),
			fixture.Exec("CREATE AGGREGATE ac_dep(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=ac_dep_merge,SORTOP = OPERATOR(public.>#))", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM (SELECT oid FROM pg_proc WHERE proname='ac_dep') p JOIN pg_depend d ON d.objid=p.oid JOIN pg_proc f ON f.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_proc'::regclass AND f.proname='ac_dep_merge' AND d.deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM (SELECT oid FROM pg_proc WHERE proname='ac_dep') p JOIN pg_depend d ON d.objid=p.oid JOIN pg_operator o ON o.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_operator'::regclass AND o.oprname='>#' AND d.deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		createAggregateRun(t, p, b, c,
			fixture.ImplicitError("DROP FUNCTION ac_dep_merge(integer,integer)", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function ac_dep_merge(integer,integer) because other objects depend on it", Detail: "function ac_dep(integer) depends on function ac_dep_merge(integer,integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
			fixture.ImplicitError("DROP OPERATOR public.># (integer,integer)", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop operator >#(integer,integer) because other objects depend on it", Detail: "function ac_dep(integer) depends on operator >#(integer,integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
			fixture.Exec("ALTER FUNCTION ac_dep_merge(integer,integer) RENAME TO ac_dep_merge_renamed", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT f.proname FROM (SELECT oid FROM pg_proc WHERE proname='ac_dep') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggcombinefn", []string{"proname"}, []string{"NAME"}, [][]any{{"ac_dep_merge_renamed"}}))
		createAggregateRun(t, p, b, c,
			fixture.Exec("DROP OPERATOR public.># (integer,integer) CASCADE", 0),
			fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ac_dep'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
