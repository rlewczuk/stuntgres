package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_6 fmgr_security_definer and plpgsql's trigger
// entry. Trigger firing deliberately does not perform ordinary EXECUTE checks.
func TestReferenceRoutineAttributesTriggerEntry(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_tr_actor", "ra_tr_owner", "ra_tr_next"})
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		const auditSQL = "INSERT INTO public.ra_tr_audit VALUES(NEW.id,current_user::text,session_user::text,current_setting('application_name'),current_setting('search_path'))"
		userOperatorExec(t, p, b, c,
			"CREATE ROLE ra_tr_actor", "CREATE ROLE ra_tr_owner", "CREATE ROLE ra_tr_next",
			"GRANT USAGE ON SCHEMA public TO ra_tr_actor,ra_tr_owner,ra_tr_next",
			"CREATE TABLE ra_tr_data(id integer,v integer)",
			"CREATE TABLE ra_tr_path(v integer)",
			"CREATE TABLE ra_tr_audit(id integer,who text,auth text,app text,path text)",
			"GRANT SELECT,INSERT ON ra_tr_data TO ra_tr_actor",
			"GRANT SELECT ON ra_tr_path TO ra_tr_actor,ra_tr_owner,ra_tr_next",
			"GRANT INSERT ON ra_tr_audit TO ra_tr_owner",
			"CREATE FUNCTION ra_tr_entry() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET application_name='trigger-inside' SET search_path=public,pg_catalog AS $$BEGIN "+auditSQL+"; NEW.v := NEW.v+(SELECT v FROM ra_tr_path); IF NEW.id<0 THEN RAISE EXCEPTION 'trigger rejected'; END IF; RETURN NEW; END$$",
			"ALTER FUNCTION ra_tr_entry() OWNER TO ra_tr_owner",
			"CREATE TRIGGER ra_tr_entry BEFORE INSERT ON ra_tr_data FOR EACH ROW EXECUTE FUNCTION ra_tr_entry()")
		run(fixture.Exec("INSERT INTO ra_tr_path VALUES(7)", 1))
		userOperatorExec(t, p, b, c, "SET application_name='trigger-outside'", "SET search_path=pg_catalog", "SET SESSION AUTHORIZATION ra_tr_actor")
		q := "INSERT INTO public.ra_tr_data VALUES($1,$2) RETURNING id,v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fire := func(id int64, prepared bool) {
			s := triggerRows(q, []any{id, int64(17)})
			s.Args = []any{id, int64(10)}
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			} else {
				run(s)
			}
		}
		restored := func() {
			run(fixture.Select("SELECT current_user::text AS who,session_user::text AS auth,current_setting('application_name') AS app,current_setting('search_path') AS path", []string{"who", "auth", "app", "path"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ra_tr_actor", "ra_tr_actor", "trigger-outside", "pg_catalog"}}))
		}
		fire(1, false)
		restored()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION public.ra_tr_entry() FROM PUBLIC", "SET SESSION AUTHORIZATION ra_tr_actor")
		fire(2, true)
		restored()
		d := fixture.ControlsReferenceDiagnostic("P0001", "trigger rejected")
		d.Context = "PL/pgSQL function ra_tr_entry() line 1 at RAISE"
		s := fixture.ErrorDiagnostic(q, d)
		s.ErrorStage = "execute"
		s.Args = []any{int64(-1), int64(10)}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		restored()
		denied := func() {
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ra_tr_audit")
			d.Context = "SQL statement \"" + auditSQL + "\"\nPL/pgSQL function ra_tr_entry() line 1 at SQL statement"
			s := fixture.ErrorDiagnostic(q, d)
			s.ErrorStage = "execute"
			s.Args = []any{int64(9), int64(10)}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			restored()
		}
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER FUNCTION public.ra_tr_entry() SECURITY INVOKER", "SET SESSION AUTHORIZATION ra_tr_actor")
		denied()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT INSERT ON public.ra_tr_audit TO ra_tr_actor", "SET SESSION AUTHORIZATION ra_tr_actor")
		fire(3, true)
		restored()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER FUNCTION public.ra_tr_entry() SECURITY DEFINER SET application_name='trigger-next' SET search_path=pg_catalog,public", "ALTER FUNCTION public.ra_tr_entry() OWNER TO ra_tr_next", "SET SESSION AUTHORIZATION ra_tr_actor")
		denied()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT INSERT ON public.ra_tr_audit TO ra_tr_next", "SET SESSION AUTHORIZATION ra_tr_actor")
		fire(4, true)
		fire(5, false)
		restored()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(triggerRows("SELECT id,v FROM public.ra_tr_data ORDER BY id", []any{int64(1), int64(17)}, []any{int64(2), int64(17)}, []any{int64(3), int64(17)}, []any{int64(4), int64(17)}, []any{int64(5), int64(17)}))
		run(fixture.Select("SELECT id,who,auth,app,path FROM public.ra_tr_audit ORDER BY id", []string{"id", "who", "auth", "app", "path"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
			{int64(1), "ra_tr_owner", "ra_tr_actor", "trigger-inside", "public, pg_catalog"},
			{int64(2), "ra_tr_owner", "ra_tr_actor", "trigger-inside", "public, pg_catalog"},
			{int64(3), "ra_tr_actor", "ra_tr_actor", "trigger-inside", "public, pg_catalog"},
			{int64(4), "ra_tr_next", "ra_tr_actor", "trigger-next", "pg_catalog, public"},
			{int64(5), "ra_tr_next", "ra_tr_actor", "trigger-next", "pg_catalog, public"},
		}))
	})
}

func TestReferenceRoutineAttributesTriggerVolatility(t *testing.T) {
	for _, volatility := range []string{"STABLE", "IMMUTABLE"} {
		t.Run(volatility, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				userOperatorExec(t, p, b, c,
					"CREATE TABLE ra_tr_vol_data(id integer,v integer)", "CREATE TABLE ra_tr_vol_log(v integer)",
					fmt.Sprintf("CREATE FUNCTION ra_tr_vol() RETURNS trigger LANGUAGE plpgsql %s SET application_name='nonvolatile-trigger' AS $$BEGIN INSERT INTO public.ra_tr_vol_log VALUES(NEW.id); RETURN NEW; END$$", volatility),
					"CREATE TRIGGER ra_tr_vol BEFORE INSERT ON ra_tr_vol_data FOR EACH ROW EXECUTE FUNCTION ra_tr_vol()",
					"SET application_name='volatility-outside'")
				q := "INSERT INTO ra_tr_vol_data VALUES(1,10)"
				d := fixture.ControlsReferenceDiagnostic("0A000", "INSERT is not allowed in a non-volatile function")
				d.Context = "SQL statement \"INSERT INTO public.ra_tr_vol_log VALUES(NEW.id)\"\nPL/pgSQL function ra_tr_vol() line 1 at SQL statement"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					s.Prepared = prepared
					run(s)
				}
				run(triggerRows("SELECT id,v FROM ra_tr_vol_data"))
				run(fixture.Select("SELECT v FROM ra_tr_vol_log", []string{"v"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT current_setting('application_name') AS app", []string{"app"}, []string{"TEXT"}, [][]any{{"volatility-outside"}}))
				userOperatorExec(t, p, b, c, "ALTER FUNCTION ra_tr_vol() VOLATILE")
				run(fixture.Prepared(fixture.Exec(q, 1)))
				run(triggerRows("SELECT id,v FROM ra_tr_vol_data", []any{int64(1), int64(10)}))
				run(fixture.Select("SELECT v FROM ra_tr_vol_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceRoutineAttributesTriggerRoutes(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_route_actor", "ra_route_owner"})
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("trigger route prepared=%v SQL=%s args=%#v", s.Prepared, s.Query, s.Args)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		userOperatorExec(t, p, b, c,
			"CREATE ROLE ra_route_actor", "CREATE ROLE ra_route_owner",
			"GRANT USAGE ON SCHEMA public TO ra_route_actor,ra_route_owner",
			"CREATE TABLE ra_route_data(id integer,v integer)",
			"CREATE TABLE ra_route_secret(v integer)",
			"CREATE TABLE ra_route_audit(op text,who text,role_name text,auth text,app text,v integer)",
			"GRANT SELECT,INSERT,UPDATE,DELETE ON ra_route_data TO ra_route_actor",
			"GRANT SELECT ON ra_route_secret TO ra_route_owner",
			"GRANT INSERT ON ra_route_audit TO ra_route_owner",
			"CREATE FUNCTION ra_route_fn() RETURNS trigger LANGUAGE plpgsql STRICT SECURITY DEFINER SET application_name='route-inside' AS $$BEGIN BEGIN PERFORM set_config('application_name','caught-inner',true); RAISE EXCEPTION 'caught'; EXCEPTION WHEN raise_exception THEN PERFORM 1; END; INSERT INTO public.ra_route_audit SELECT TG_OP,current_user::text,current_role::text,session_user::text,current_setting('application_name'),v FROM public.ra_route_secret; IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END$$",
			"ALTER FUNCTION ra_route_fn() OWNER TO ra_route_owner",
			"CREATE TRIGGER ra_route BEFORE INSERT OR UPDATE OR DELETE ON ra_route_data FOR EACH ROW EXECUTE FUNCTION ra_route_fn('argument')",
			"SET application_name='route-outside'")
		run(fixture.Exec("INSERT INTO ra_route_secret VALUES(42)", 1))
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_route_actor")
		// NULL row members are not ordinary function arguments: STRICT must
		// not skip any real trigger invocation, including DELETE's absent NEW.
		run(fixture.Exec("INSERT INTO ra_route_data VALUES(1,NULL)", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE ra_route_data SET v=NULL WHERE id=1", 1)))
		run(fixture.Exec("DELETE FROM ra_route_data WHERE id=1", 1))
		run(fixture.Select("SELECT current_user::text AS who,current_setting('application_name') AS app", []string{"who", "app"}, []string{"TEXT", "TEXT"}, [][]any{{"ra_route_actor", "route-outside"}}))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(fixture.Select("SELECT op,who,role_name,auth,app,v FROM ra_route_audit ORDER BY op", []string{"op", "who", "role_name", "auth", "app", "v"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4"}, [][]any{
			{"DELETE", "ra_route_owner", "ra_route_owner", "ra_route_actor", "route-inside", int64(42)},
			{"INSERT", "ra_route_owner", "ra_route_owner", "ra_route_actor", "route-inside", int64(42)},
			{"UPDATE", "ra_route_owner", "ra_route_owner", "ra_route_actor", "route-inside", int64(42)},
		}))
		// A nonvolatile, read-only trigger remains legal on all three routes.
		userOperatorExec(t, p, b, c,
			"CREATE OR REPLACE FUNCTION ra_route_fn() RETURNS trigger LANGUAGE plpgsql STABLE SECURITY DEFINER SET application_name='readonly-inside' AS $$DECLARE n integer; BEGIN SELECT v INTO n FROM public.ra_route_secret; IF n<>42 OR current_setting('application_name')<>'readonly-inside' THEN RAISE EXCEPTION 'wrong readonly environment'; END IF; IF TG_OP='DELETE' THEN RETURN OLD; END IF; NEW.v := n; RETURN NEW; END$$",
			"SET SESSION AUTHORIZATION ra_route_actor")
		run(triggerRows("INSERT INTO ra_route_data VALUES(2,NULL) RETURNING id,v", []any{int64(2), int64(42)}))
		run(triggerRows("UPDATE ra_route_data SET v=NULL WHERE id=2 RETURNING id,v", []any{int64(2), int64(42)}))
		run(triggerRows("DELETE FROM ra_route_data WHERE id=2 RETURNING id,v", []any{int64(2), int64(42)}))
		run(fixture.Select("SELECT current_setting('application_name') AS app", []string{"app"}, []string{"TEXT"}, [][]any{{"route-outside"}}))
	})
}
