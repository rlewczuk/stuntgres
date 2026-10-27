package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// A CREATE TABLE-only filter prevents the callback from intercepting its own
// ALTER FUNCTION lifecycle or teardown. eventTriggerScenario retires the global
// hook before public/schema/role fixture cleanup, including on failed assertions.
func TestReferenceRoutineAttributesEventTriggerEntry(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_et_actor", "ra_et_owner", "ra_et_next"})
	eventTriggerScenario(t, []string{"ra_et_entry"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		exec := func(queries ...string) {
			for _, q := range queries {
				run(fixture.Exec(q, 0))
			}
		}
		const auditSQL = "INSERT INTO public.ra_et_audit VALUES(current_user::text,session_user::text,current_setting('application_name'),current_setting('search_path'))"
		const initialPath = `public, pg_catalog, "comma,name", "quote""name"`
		const nextPath = `pg_catalog, public, "comma,name", "quote""name"`
		exec("CREATE ROLE ra_et_actor", "CREATE ROLE ra_et_owner", "CREATE ROLE ra_et_next",
			"GRANT USAGE ON SCHEMA public TO ra_et_actor,ra_et_owner,ra_et_next",
			"GRANT CREATE ON SCHEMA public TO ra_et_actor",
			"CREATE TABLE ra_et_audit(who text,auth text,app text,path text)",
			"CREATE TABLE ra_et_path(v integer)",
			"GRANT SELECT ON ra_et_path TO ra_et_actor,ra_et_owner,ra_et_next",
			"GRANT INSERT ON ra_et_audit TO ra_et_owner",
			"CREATE FUNCTION ra_et_entry() RETURNS event_trigger LANGUAGE plpgsql SECURITY DEFINER SET application_name='event-inside' SET search_path=public,pg_catalog,\"comma,name\",\"quote\"\"name\" AS $$BEGIN "+auditSQL+"; PERFORM v FROM ra_et_path; IF current_setting('application_name')='event-error' THEN RAISE EXCEPTION 'event rejected'; END IF; END$$",
			"ALTER FUNCTION ra_et_entry() OWNER TO ra_et_owner")
		run(fixture.Exec("INSERT INTO ra_et_path VALUES(7)", 1))
		eventTriggerCreate(t, p, b, c, "ra_et_entry", "ddl_command_end", "'CREATE TABLE'", "ra_et_entry")
		exec("SET application_name='event-outside'", "SET search_path=pg_catalog", "SET SESSION AUTHORIZATION ra_et_actor")
		q := "CREATE TABLE public.ra_et_target(id integer)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		restored := func() {
			run(fixture.Select("SELECT current_user::text AS who,session_user::text AS auth,current_setting('application_name') AS app,current_setting('search_path') AS path", []string{"who", "auth", "app", "path"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ra_et_actor", "ra_et_actor", "event-outside", "pg_catalog"}}))
		}
		catalog := func(n int64) {
			run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='ra_et_target' AND relnamespace='public'::regnamespace", n))
		}
		fire := func(prepared bool) {
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			} else {
				run(fixture.Exec(q, 0))
			}
			restored()
			catalog(1)
			run(fixture.Select("SELECT id FROM public.ra_et_target", []string{"id"}, []string{"INT4"}, nil))
			exec("DROP TABLE public.ra_et_target")
			catalog(0)
		}
		fire(false)
		// PostgreSQL checks EXECUTE when the event trigger is declared, not as
		// an ordinary function-call ACL check on each subsequent DDL firing.
		exec("RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION public.ra_et_entry() FROM PUBLIC", "SET SESSION AUTHORIZATION ra_et_actor")
		fire(true)
		denied := func() {
			d := eventTriggerDiagnostic("42501", "permission denied for table ra_et_audit")
			d.Context = "SQL statement \"" + auditSQL + "\"\nPL/pgSQL function ra_et_entry() line 1 at SQL statement"
			s := fixture.ErrorDiagnostic(q, d)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			restored()
			catalog(0)
		}
		exec("RESET SESSION AUTHORIZATION", "ALTER FUNCTION public.ra_et_entry() SECURITY INVOKER", "SET SESSION AUTHORIZATION ra_et_actor")
		denied()
		exec("RESET SESSION AUTHORIZATION", "GRANT INSERT ON public.ra_et_audit TO ra_et_actor", "SET SESSION AUTHORIZATION ra_et_actor")
		fire(true)
		exec("RESET SESSION AUTHORIZATION", "ALTER FUNCTION public.ra_et_entry() SECURITY DEFINER SET application_name='event-next' SET search_path=pg_catalog,public,\"comma,name\",\"quote\"\"name\"", "ALTER FUNCTION public.ra_et_entry() OWNER TO ra_et_next", "SET SESSION AUTHORIZATION ra_et_actor")
		denied()
		exec("RESET SESSION AUTHORIZATION", "GRANT INSERT ON public.ra_et_audit TO ra_et_next", "SET SESSION AUTHORIZATION ra_et_actor")
		fire(true)
		fire(false)
		exec("RESET SESSION AUTHORIZATION", "ALTER FUNCTION public.ra_et_entry() SET application_name='event-error'", "SET SESSION AUTHORIZATION ra_et_actor")
		d := eventTriggerFailure("ra_et_entry", "event rejected")
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.ErrorStage = "execute"
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			} else {
				run(s)
			}
			restored()
			catalog(0)
		}
		exec("RESET SESSION AUTHORIZATION")
		// No row from either rejected CREATE survives; the created relation and
		// the callback's protected INSERT are one atomic utility effect.
		run(fixture.Select("SELECT who,auth,app,path FROM public.ra_et_audit ORDER BY who,app,path", []string{"who", "auth", "app", "path"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
			{"ra_et_actor", "ra_et_actor", "event-inside", initialPath},
			{"ra_et_next", "ra_et_actor", "event-next", nextPath},
			{"ra_et_next", "ra_et_actor", "event-next", nextPath},
			{"ra_et_owner", "ra_et_actor", "event-inside", initialPath},
			{"ra_et_owner", "ra_et_actor", "event-inside", initialPath},
		}))
		// A failed callback must not poison the held utility's next invocation.
		exec("ALTER FUNCTION public.ra_et_entry() SET application_name='event-recovered'", "SET SESSION AUTHORIZATION ra_et_actor")
		fire(true)
		exec("RESET SESSION AUTHORIZATION")
		run(eventTriggerCount("SELECT count(*) AS n FROM public.ra_et_audit", 6))
		if !b.PG {
			// Native replacement must reactivate the phase only after replay,
			// preserving the live function owner, configuration and held DDL.
			fixture.CheckRoundtrip(t, p, fixture.CheckDump(t, p))
			run(eventTriggerCount("SELECT count(*) AS n FROM public.ra_et_audit", 6))
			exec("SET SESSION AUTHORIZATION ra_et_actor")
			fire(true)
			exec("RESET SESSION AUTHORIZATION")
			run(eventTriggerCount(fmt.Sprintf("SELECT count(*) AS n FROM public.ra_et_audit WHERE who='ra_et_next' AND auth='ra_et_actor' AND app='event-recovered' AND path='%s'", nextPath), 2))
		}
	})
}

// Each phase is reached by its real utility route, not a direct callback hook.
// The same protected INSERT distinguishes invoker admission from definer entry
// and also proves that each phase installs its own nonvolatile command policy.
func TestReferenceRoutineAttributesEventTriggerPhases(t *testing.T) {
	for _, tc := range []struct {
		phase, tag, command string
	}{
		{"ddl_command_start", "CREATE TABLE", "CREATE TABLE public.ra_ep_created(id integer)"},
		{"ddl_command_end", "CREATE TABLE", "CREATE TABLE public.ra_ep_created(id integer)"},
		{"sql_drop", "DROP TABLE", "DROP TABLE public.ra_ep_target"},
		{"table_rewrite", "ALTER TABLE", "ALTER TABLE public.ra_ep_target ALTER COLUMN v TYPE varchar(4)"},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_ep_actor", "ra_ep_owner"})
			eventTriggerScenario(t, []string{"ra_ep_hook"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				exec := func(queries ...string) {
					for _, q := range queries {
						run(fixture.Exec(q, 0))
					}
				}
				const body = "INSERT INTO public.ra_ep_audit SELECT TG_EVENT,TG_TAG,current_user::text,current_role::text,session_user::text,current_setting('application_name'),v FROM public.ra_ep_secret"
				exec("CREATE ROLE ra_ep_actor", "CREATE ROLE ra_ep_owner",
					"GRANT USAGE ON SCHEMA public TO ra_ep_actor,ra_ep_owner",
					"GRANT CREATE ON SCHEMA public TO ra_ep_actor",
					"CREATE TABLE ra_ep_secret(v integer)",
					"CREATE TABLE ra_ep_audit(event text,tag text,who text,role_name text,auth text,app text,v integer)",
					"CREATE TABLE ra_ep_target(v varchar(8))",
					"ALTER TABLE ra_ep_target OWNER TO ra_ep_actor",
					"GRANT SELECT ON ra_ep_secret TO ra_ep_owner",
					"GRANT INSERT ON ra_ep_audit TO ra_ep_owner",
					"CREATE FUNCTION ra_ep_fn() RETURNS event_trigger LANGUAGE plpgsql SECURITY DEFINER SET application_name='phase-inside' AS $$BEGIN BEGIN PERFORM set_config('application_name','caught-inner',true); RAISE EXCEPTION 'caught'; EXCEPTION WHEN raise_exception THEN PERFORM 1; END; "+body+"; END$$",
					"ALTER FUNCTION ra_ep_fn() OWNER TO ra_ep_owner")
				run(fixture.Exec("INSERT INTO ra_ep_secret VALUES(42)", 1))
				run(fixture.Exec("INSERT INTO ra_ep_target VALUES('abcd')", 1))
				eventTriggerCreate(t, p, b, c, "ra_ep_hook", tc.phase, "'"+tc.tag+"'", "ra_ep_fn")
				exec("ALTER FUNCTION ra_ep_fn() STABLE", "SET application_name='phase-outside'", "SET SESSION AUTHORIZATION ra_ep_actor")
				d := eventTriggerDiagnostic("0A000", "INSERT is not allowed in a non-volatile function")
				d.Context = "SQL statement \"" + body + "\"\nPL/pgSQL function ra_ep_fn() line 1 at SQL statement"
				s := fixture.ErrorDiagnostic(tc.command, d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT current_user::text AS who,current_setting('application_name') AS app", []string{"who", "app"}, []string{"TEXT", "TEXT"}, [][]any{{"ra_ep_actor", "phase-outside"}}))
				exec("RESET SESSION AUTHORIZATION")
				run(eventTriggerCount("SELECT count(*) AS n FROM ra_ep_audit", 0))
				// A rejected drop/rewrite leaves the original relation and data.
				run(fixture.Select("SELECT v FROM ra_ep_target", []string{"v"}, []string{"VARCHAR"}, [][]any{{"abcd"}}))
				exec("ALTER FUNCTION ra_ep_fn() VOLATILE", "SET SESSION AUTHORIZATION ra_ep_actor")
				run(fixture.Prepared(fixture.Exec(tc.command, 0)))
				run(fixture.Select("SELECT current_user::text AS who,current_setting('application_name') AS app", []string{"who", "app"}, []string{"TEXT", "TEXT"}, [][]any{{"ra_ep_actor", "phase-outside"}}))
				exec("RESET SESSION AUTHORIZATION")
				run(fixture.Select("SELECT event,tag,who,role_name,auth,app,v FROM ra_ep_audit", []string{"event", "tag", "who", "role_name", "auth", "app", "v"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4"}, [][]any{{tc.phase, tc.tag, "ra_ep_owner", "ra_ep_owner", "ra_ep_actor", "phase-inside", int64(42)}}))
				relation, count := "ra_ep_target", int64(1)
				if tc.tag == "CREATE TABLE" {
					relation = "ra_ep_created"
				} else if tc.phase == "sql_drop" {
					count = 0
				}
				run(eventTriggerCount(fmt.Sprintf("SELECT count(*) AS n FROM pg_class WHERE relname='%s' AND relnamespace='public'::regnamespace", relation), count))
			})
		})
	}
}
