package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateMovingAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ma_owner", "ma_reader"})
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		lifecycleExec(t, p, b, c,
			"CREATE ROLE ma_owner", "CREATE ROLE ma_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO ma_owner", "GRANT USAGE ON SCHEMA public TO ma_reader",
			"GRANT SELECT ON ma_input TO ma_reader",
			"CREATE FUNCTION ma_regular(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION ma_forward(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION ma_inverse(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1-$2$$",
			"CREATE FUNCTION ma_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		const ddl = "CREATE AGGREGATE ma_rejected(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC=ma_final)"
		for _, support := range []string{"ma_forward(integer,integer)", "ma_inverse(integer,integer)", "ma_final(integer)"} {
			lifecycleExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+support+" FROM PUBLIC", "SET SESSION AUTHORIZATION ma_owner")
			name := "ma_final"
			if support != "ma_final(integer)" {
				name = support[:len(support)-len("(integer,integer)")]
			}
			lifecycleError(t, p, b, c, ddl, "42501", "permission denied for function "+name)
			movingNoResidue(t, p, b, c)
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION "+support+" TO PUBLIC")
		}
		lifecycleExec(t, p, b, c,
			"REVOKE EXECUTE ON FUNCTION ma_regular(integer,integer),ma_forward(integer,integer),ma_inverse(integer,integer),ma_final(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ma_regular(integer,integer),ma_forward(integer,integer),ma_inverse(integer,integer),ma_final(integer) TO ma_owner",
			"SET SESSION AUTHORIZATION ma_owner", ddl,
			"REVOKE EXECUTE ON FUNCTION ma_rejected(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ma_rejected(integer) TO ma_reader",
			"RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION ma_reader")
		const q = "SELECT id,ma_rejected(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input WHERE id<=$1 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func() {
			for _, limit := range []int64{4, 0} {
				values := []any{int64(2), int64(5), int64(8), int64(12)}
				if limit == 0 {
					values = nil
				}
				s := movingRows(q, values...)
				s.Args = []any{limit}
				createAggregateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		check() // aggregate EXECUTE, not direct support EXECUTE, authorizes the reader.
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ma_inverse(integer,integer) FROM ma_owner", "SET SESSION AUTHORIZATION ma_reader")
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ma_rejected(x) AS v FROM ma_input", int64(17)))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_rejected(x) OVER(ORDER BY id ROWS UNBOUNDED PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(10), int64(17)))
		for _, limit := range []int64{4, 0} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ma_inverse"}, limit)
			s.SelectRows, s.ErrorStage = true, "execute"
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ma_inverse(integer,integer) TO ma_owner", "REVOKE EXECUTE ON FUNCTION ma_regular(integer,integer) FROM ma_owner", "SET SESSION AUTHORIZATION ma_reader")
		check() // Unused ordinary support must not be admitted in moving mode.
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ma_regular(integer,integer) TO ma_owner", "REVOKE EXECUTE ON FUNCTION ma_rejected(integer) FROM ma_reader", "SET SESSION AUTHORIZATION ma_reader")
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ma_rejected"}, 0)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION",
			"CREATE DOMAIN ma_state AS integer",
			"CREATE FUNCTION ma_domain_forward(ma_state,integer) RETURNS ma_state LANGUAGE SQL AS $$SELECT ($1::integer+$2)::ma_state$$",
			"CREATE FUNCTION ma_domain_inverse(ma_state,integer) RETURNS ma_state LANGUAGE SQL AS $$SELECT ($1::integer-$2)::ma_state$$",
			"CREATE FUNCTION ma_domain_final(ma_state) RETURNS integer LANGUAGE SQL AS $$SELECT $1::integer$$",
			"REVOKE USAGE ON TYPE ma_state FROM PUBLIC", "SET SESSION AUTHORIZATION ma_owner")
		const domainDDL = "CREATE AGGREGATE ma_domain(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',MSFUNC=ma_domain_forward,MINVFUNC=ma_domain_inverse,MSTYPE=ma_state,MINITCOND='0',MFINALFUNC=ma_domain_final)"
		lifecycleError(t, p, b, c, domainDDL, "42501", "permission denied for type ma_state")
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE ma_state TO ma_owner", "SET SESSION AUTHORIZATION ma_owner", domainDDL, "RESET SESSION AUTHORIZATION",
			"CREATE TABLE ma_private(v integer)",
			"CREATE FUNCTION ma_body_inverse(integer,integer) RETURNS integer BEGIN ATOMIC SELECT $1-$2+v FROM ma_private; END",
			"GRANT SELECT ON ma_private TO ma_owner", "SET SESSION AUTHORIZATION ma_owner",
			"CREATE AGGREGATE ma_body(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_body_inverse,MSTYPE=int4,MINITCOND='0')", "RESET SESSION AUTHORIZATION")
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ma_private VALUES(0)", 1))
		lifecycleExec(t, p, b, c, "SET SESSION AUTHORIZATION ma_reader")
		body := fixture.ImplicitError("SELECT ma_body(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY v", fixture.Diagnostic{Code: "42501", Message: "permission denied for table ma_private", Context: "SQL function \"ma_body_inverse\" statement 1"})
		body.SelectRows, body.ErrorStage = true, "execute"
		createAggregateBoth(t, p, b, c, body)
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON ma_private TO ma_reader", "SET SESSION AUTHORIZATION ma_reader")
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_body(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(8), int64(12)))
		lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	})
}

func TestReferenceAggregateMovingDependencies(t *testing.T) {
	t.Run("moving_state", movingStateDependency)
	for _, support := range []string{"ma_forward", "ma_inverse", "ma_final"} {
		t.Run(support, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				movingSetup(t, p, b, c)
				lifecycleExec(t, p, b, c,
					"CREATE FUNCTION ma_forward(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
					"CREATE FUNCTION ma_inverse(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1-$2$$",
					"CREATE FUNCTION ma_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
					"CREATE AGGREGATE ma_owned(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC=ma_final)")
				args := "integer,integer"
				if support == "ma_final" {
					args = "integer"
				}
				sig := support + "(" + args + ")"
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT d.deptype::text AS kind FROM (SELECT oid FROM pg_proc WHERE proname='ma_owned') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_proc'::regclass AND d.refobjid='"+sig+"'::regprocedure", []string{"kind"}, []string{"TEXT"}, [][]any{{"n"}}))
				printed := sig
				createAggregateRun(t, p, b, c, fixture.ImplicitError("DROP FUNCTION "+sig, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function " + printed + " because other objects depend on it", Detail: "function ma_owned(integer) depends on function " + printed, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
				createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_owned(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(8), int64(12)))
				lifecycleExec(t, p, b, c, "DROP FUNCTION "+sig+" CASCADE")
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ma_owned'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}
