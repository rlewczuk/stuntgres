package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateFinalAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_owner", "fa_reader"})
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
		for _, q := range []string{
			"CREATE ROLE fa_owner", "CREATE ROLE fa_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO fa_owner", "GRANT USAGE ON SCHEMA public TO fa_reader",
			"CREATE FUNCTION fa_final(integer) RETURNS text LANGUAGE SQL AS $$SELECT coalesce($1::text,'empty')$$",
			"REVOKE EXECUTE ON FUNCTION fa_final(integer) FROM PUBLIC",
			"SET SESSION AUTHORIZATION fa_owner",
		} {
			exec(q)
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		const ddl = "CREATE AGGREGATE fa_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=fa_final)"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(ddl, fixture.Diagnostic{Code: "42501", Message: "permission denied for function fa_final"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			aggregateFinalNoResidue(t, p, b, c)
		}
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION fa_final(integer) TO fa_owner", "SET SESSION AUTHORIZATION fa_owner", ddl, "DROP ROUTINE fa_rejected(integer)", "CREATE AGGREGATE fa_owned(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=fa_final)", "REVOKE EXECUTE ON FUNCTION fa_owned(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION fa_owned(integer) TO fa_reader", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION fa_reader"} {
			exec(q)
		}
		const q = "SELECT fa_owned(x) AS v FROM(VALUES(1),(2),(3)) s(x) WHERE x<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func() {
			for _, tc := range []struct {
				limit int64
				want  string
			}{{3, "6"}, {0, "empty"}} {
				s := aggregateFinalText(q, tc.want, tc.limit)
				createAggregateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		// Reader has no direct final EXECUTE, but aggregate EXECUTE suffices.
		check()
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION fa_final(integer) FROM fa_owner", "SET SESSION AUTHORIZATION fa_reader"} {
			exec(sql)
		}
		for _, limit := range []int64{3, 0} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function fa_final"}, limit)
			s.SelectRows, s.ErrorStage = true, "execute"
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION fa_final(integer) TO fa_owner", "SET SESSION AUTHORIZATION fa_reader"} {
			exec(sql)
		}
		check()
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION fa_owned(integer) FROM fa_reader", "SET SESSION AUTHORIZATION fa_reader"} {
			exec(sql)
		}
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate fa_owned"}, 0)
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		exec("RESET SESSION AUTHORIZATION")
		for _, sql := range []string{
			"CREATE TABLE fa_private(v integer)",
			"CREATE FUNCTION fa_body_final(integer) RETURNS text BEGIN ATOMIC SELECT ($1+v)::text FROM fa_private; END",
			"REVOKE EXECUTE ON FUNCTION fa_body_final(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION fa_body_final(integer) TO fa_owner",
			"GRANT SELECT ON fa_private TO fa_owner",
			"SET SESSION AUTHORIZATION fa_owner",
			"CREATE AGGREGATE fa_body(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_body_final)",
			"RESET SESSION AUTHORIZATION",
		} {
			exec(sql)
		}
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO fa_private VALUES(10)", 1))
		exec("SET SESSION AUTHORIZATION fa_reader")
		body := fixture.ImplicitError("SELECT fa_body(x) AS v FROM(VALUES(1)) s(x)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table fa_private", Context: "SQL function \"fa_body_final\" statement 1"})
		body.SelectRows, body.ErrorStage = true, "execute"
		createAggregateBoth(t, p, b, c, body)
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "GRANT SELECT ON fa_private TO fa_reader", "SET SESSION AUTHORIZATION fa_reader"} {
			exec(sql)
		}
		createAggregateBoth(t, p, b, c, aggregateFinalText(body.Query, "11"))
		exec("RESET SESSION AUTHORIZATION")
		for _, sql := range []string{
			"CREATE DOMAIN fa_result AS text",
			"CREATE FUNCTION fa_domain_final(integer) RETURNS fa_result LANGUAGE SQL AS $$SELECT $1::text::fa_result$$",
			"REVOKE USAGE ON TYPE fa_result FROM PUBLIC",
			"SET SESSION AUTHORIZATION fa_owner",
		} {
			exec(sql)
		}
		const domainDDL = "CREATE AGGREGATE fa_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_domain_final)"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(domainDDL, fixture.Diagnostic{Code: "42501", Message: "permission denied for type fa_result"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			aggregateFinalNoResidue(t, p, b, c)
		}
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE fa_result TO fa_owner", "SET SESSION AUTHORIZATION fa_owner", domainDDL} {
			exec(sql)
		}
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_rejected(x)::text AS v FROM(VALUES(1),(2),(3)) s(x)", "6"))
		exec("RESET SESSION AUTHORIZATION")
	})
	t.Run("strict_null_admission", aggregateFinalStrictAuthority)
}

func TestReferenceAggregateFinalDependencies(t *testing.T) {
	for _, target := range []string{"final", "result"} {
		t.Run(target, func(t *testing.T) {
			aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE DOMAIN fa_result AS text", 0),
					fixture.Exec("CREATE FUNCTION fa_final(integer) RETURNS fa_result LANGUAGE SQL AS $$SELECT $1::text::fa_result$$", 0),
					fixture.Exec("CREATE AGGREGATE fa_owned(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_final)", 0))
				for _, edge := range []struct{ class, ref string }{{"pg_proc", "'fa_final(integer)'::regprocedure"}, {"pg_type", "'fa_result'::regtype"}} {
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT d.deptype::text AS kind FROM (SELECT oid FROM pg_proc WHERE proname='fa_owned') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='"+edge.class+"'::regclass AND d.refobjid="+edge.ref, []string{"kind"}, []string{"TEXT"}, [][]any{{"n"}}))
				}
				q := "DROP FUNCTION fa_final(integer)"
				d := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function fa_final(integer) because other objects depend on it", Detail: "function fa_owned(integer) depends on function fa_final(integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				if target == "result" {
					q = "DROP DOMAIN fa_result"
					d.Message = "cannot drop type fa_result because other objects depend on it"
					d.Detail = "function fa_final(integer) depends on type fa_result\nfunction fa_owned(integer) depends on type fa_result"
				}
				createAggregateRun(t, p, b, c, fixture.ImplicitError(q, d))
				createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_owned(x)::text AS v FROM(VALUES(1),(2),(3)) s(x)", "6"))
				createAggregateRun(t, p, b, c, fixture.Exec(q+" CASCADE", 0))
				createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname IN('fa_final','fa_owned')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}

func aggregateFinalStrictAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fa_strict_owner"})
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
		for _, q := range []string{
			"CREATE ROLE fa_strict_owner",
			"GRANT USAGE,CREATE ON SCHEMA public TO fa_strict_owner",
			"SET SESSION AUTHORIZATION fa_strict_owner",
			"CREATE AGGREGATE fa_strict_owned(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=pg_catalog.abs)",
			"RESET SESSION AUTHORIZATION",
		} {
			exec(q)
		}
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
				return
			}
			if _, err := c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC"); err != nil {
				t.Error(err)
			}
		}()
		const q = "SELECT fa_strict_owned(x) AS v FROM(VALUES(NULL::integer)) s(x) WHERE $1::boolean"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, populated := range []bool{true, false} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, nil, populated))
		}
		exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC")
		for _, populated := range []bool{true, false} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function abs"}, populated)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC")
		for _, populated := range []bool{true, false} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, nil, populated))
		}
	})
}
