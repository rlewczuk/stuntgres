package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesSecurityContext(t *testing.T) {
	t.Run("actors_and_live_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_owner", "ra_other", "ra_caller"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE ROLE ra_owner", "CREATE ROLE ra_other", "CREATE ROLE ra_caller",
				"GRANT USAGE ON SCHEMA public TO PUBLIC",
				"CREATE TABLE ra_secret(v integer)", "ALTER TABLE ra_secret OWNER TO ra_owner")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_secret VALUES(42)", 1))
			resultsExec(t, p, b, c, "CREATE TABLE ra_null_log(v integer)", "GRANT INSERT ON ra_null_log TO ra_owner,ra_other",
				"CREATE TABLE ra_runtime_input(v integer)", "GRANT SELECT ON ra_runtime_input TO PUBLIC")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_runtime_input VALUES(NULL)", 1))
			for _, lang := range []string{"sql", "plpgsql"} {
				body := "SELECT v,current_user::text,current_role::text,session_user::text FROM public.ra_secret"
				if lang == "plpgsql" {
					body = "BEGIN SELECT v,current_user::text,current_role::text,session_user::text INTO answer,actor,role_name,session_name FROM public.ra_secret;END"
				}
				for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
					name := "ra_" + lang
					if kind == "PROCEDURE" {
						name += "_proc"
					}
					resultsExec(t, p, b, c, "CREATE "+kind+" "+name+"(OUT answer integer,OUT actor text,OUT role_name text,OUT session_name text) LANGUAGE "+lang+" SECURITY DEFINER AS $$"+body+"$$", "ALTER "+kind+" "+name+"() OWNER TO ra_owner")
				}
			}
			resultsExec(t, p, b, c,
				"CREATE FUNCTION ra_inner_invoker() RETURNS text LANGUAGE SQL AS 'SELECT current_user::text'",
				"CREATE FUNCTION ra_inner_definer() RETURNS text LANGUAGE SQL SECURITY DEFINER AS 'SELECT current_user::text'",
				"ALTER FUNCTION ra_inner_definer() OWNER TO ra_other",
				"CREATE FUNCTION ra_nested(OUT before_name text,OUT invoker_name text,OUT definer_name text,OUT after_name text) LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN before_name:=current_user::text;invoker_name:=ra_inner_invoker();definer_name:=ra_inner_definer();after_name:=current_user::text;END$$",
				"ALTER FUNCTION ra_nested() OWNER TO ra_owner",
				"CREATE FUNCTION ra_actor_fail(x integer) RETURNS integer LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN RETURN 10/x;END$$",
				"ALTER FUNCTION ra_actor_fail(integer) OWNER TO ra_other",
				"CREATE FUNCTION ra_actor_catch() RETURNS text LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN BEGIN PERFORM ra_actor_fail(0);EXCEPTION WHEN division_by_zero THEN PERFORM 1;END;RETURN current_user::text;END$$",
				"ALTER FUNCTION ra_actor_catch() OWNER TO ra_owner",
				"CREATE FUNCTION ra_null(integer) RETURNS text LANGUAGE SQL STRICT SECURITY DEFINER AS 'INSERT INTO public.ra_null_log VALUES(1);SELECT current_user::text'",
				"ALTER FUNCTION ra_null(integer) OWNER TO ra_owner",
				"SET SESSION AUTHORIZATION ra_caller")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			denied := proceduresDiagnostic("42501", "permission denied for table ra_secret", 0)
			// Relation permission checking occurs at execution, even on a held query.
			s := fixture.ErrorDiagnostic("SELECT v FROM public.ra_secret", denied)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			for _, lang := range []string{"sql", "plpgsql"} {
				attributesBoth(t, p, b, c, "SELECT * FROM ra_"+lang+"()", []string{"answer", "actor", "role_name", "session_name"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(42), "ra_owner", "ra_owner", "ra_caller"}})
				proceduresBoth(t, p, b, c, "CALL ra_"+lang+"_proc(NULL,NULL,NULL,NULL)", []string{"answer", "actor", "role_name", "session_name"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(42), "ra_owner", "ra_owner", "ra_caller"}})
			}
			attributesBoth(t, p, b, c, "SELECT * FROM ra_nested()", []string{"before_name", "invoker_name", "definer_name", "after_name"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ra_owner", "ra_owner", "ra_other", "ra_owner"}})
			attributesTextBoth(t, p, b, c, "SELECT ra_actor_catch() AS v", "ra_owner")
			attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", "ra_caller")
			readSQL := "SELECT answer+0 AS v FROM ra_sql()"
			readStmt, err := c.PrepareContext(p.Ctx, readSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer readStmt.Close()
			readHeld := fixture.HeldStatement{Executor: c, Statement: readStmt, SQL: readSQL}
			fixture.RunStep(t, p.Ctx, readHeld, b.Recorder, attributesStep(b, readSQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER FUNCTION ra_sql() OWNER TO ra_other", "SET SESSION AUTHORIZATION ra_caller")
			ownerDenied := denied
			ownerDenied.Context = `SQL function "ra_sql" statement 1`
			readError := fixture.ErrorDiagnostic(readSQL, ownerDenied)
			readError.SelectRows, readError.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, readHeld, b.Recorder, readError)
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON ra_secret TO ra_other", "SET SESSION AUTHORIZATION ra_caller")
			fixture.RunStep(t, p.Ctx, readHeld, b.Recorder, attributesStep(b, readSQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			q := "SELECT ra_null($1::integer) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, attributesStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"ra_owner"}}, 1))
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER FUNCTION ra_null(integer) OWNER TO ra_other", "SET SESSION AUTHORIZATION ra_caller")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, attributesStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"ra_other"}}, 1))
			runtimeSQL := "SELECT ra_null(v) AS v FROM ra_runtime_input"
			runtimeStmt, err := c.PrepareContext(p.Ctx, runtimeSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer runtimeStmt.Close()
			runtimeHeld := fixture.HeldStatement{Executor: c, Statement: runtimeStmt, SQL: runtimeSQL}
			fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, attributesStep(b, runtimeSQL, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ra_null(integer) FROM PUBLIC", "SET SESSION AUTHORIZATION ra_caller")
			want := proceduresDiagnostic("42501", "permission denied for function ra_null", 0)
			// A custom plan with a known NULL eliminates the STRICT call entirely.
			// Preserve the observed optimization, separately from runtime admission.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, attributesStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil))
			attributesBoth(t, p, b, c, "SELECT ra_null(NULL::integer) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}})
			attributesQueryError(t, p, b, c, "SELECT ra_null(1)", want)
			s = fixture.ErrorDiagnostic(q, want, 1)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			for _, value := range []any{nil, 1} {
				resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ra_runtime_input SET v=$1", 1, value))
				resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_caller")
				attributesQueryError(t, p, b, c, runtimeSQL, want)
				runtimeError := fixture.ErrorDiagnostic(runtimeSQL, want)
				runtimeError.SelectRows, runtimeError.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, runtimeError)
				resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				proceduresCount(t, p, b, c, "ra_null_log", 2)
				resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_caller")
			}
			attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", "ra_caller")
		})
	})
	for _, lang := range []string{"sql", "plpgsql"} {
		t.Run("invoker_error_"+lang, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_error_caller"})
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE ROLE ra_error_caller", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TABLE ra_denied(v integer)")
				body := "SELECT v FROM public.ra_denied"
				contextText := `SQL function "ra_invoker" statement 1`
				if lang == "plpgsql" {
					body = "BEGIN RETURN (SELECT v FROM public.ra_denied);END"
					contextText = "PL/pgSQL expression \"(SELECT v FROM public.ra_denied)\"\nPL/pgSQL function ra_invoker() line 1 at RETURN"
				}
				procBody := "SELECT v FROM public.ra_denied"
				if lang == "plpgsql" {
					procBody = "BEGIN SELECT v INTO answer FROM public.ra_denied;END"
				}
				resultsExec(t, p, b, c, "CREATE FUNCTION ra_invoker() RETURNS integer LANGUAGE "+lang+" SECURITY INVOKER SET application_name='guard' AS $$"+body+"$$",
					"CREATE PROCEDURE ra_invoker_proc(OUT answer integer) LANGUAGE "+lang+" SECURITY INVOKER SET application_name='guard' AS $$"+procBody+"$$", "SET SESSION AUTHORIZATION ra_error_caller")
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				want := proceduresDiagnostic("42501", "permission denied for table ra_denied", 0)
				want.Context = contextText
				attributesQueryError(t, p, b, c, "SELECT ra_invoker()", want)
				procWant := want
				procWant.Context = `SQL function "ra_invoker_proc" statement 1`
				if lang == "plpgsql" {
					procWant.Context = "SQL statement \"SELECT v " + strings.Repeat(" ", len("INTO answer")) + " FROM public.ra_denied\"\nPL/pgSQL function ra_invoker_proc() line 1 at SQL statement"
				}
				attributesQueryError(t, p, b, c, "CALL ra_invoker_proc(NULL)", procWant)
				attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", "ra_error_caller")
			})
		})
	}
	t.Run("local_userid_not_restricted", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_local_owner", "ra_local_caller"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_local_owner", "CREATE ROLE ra_local_caller", "GRANT USAGE ON SCHEMA public TO PUBLIC",
				"CREATE FUNCTION ra_temp() RETURNS integer LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN CREATE TEMP TABLE ra_temp_allowed(v integer);DROP TABLE ra_temp_allowed;RETURN 7;END$$",
				"ALTER FUNCTION ra_temp() OWNER TO ra_local_owner")
			for _, parameter := range []string{"role", "session_authorization"} {
				resultsExec(t, p, b, c, "CREATE FUNCTION ra_change_"+parameter+"() RETURNS text LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN RETURN set_config('"+parameter+"','ra_local_caller',false);END$$", "ALTER FUNCTION ra_change_"+parameter+"() OWNER TO ra_local_owner")
			}
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_local_caller")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			attributesIntBoth(t, p, b, c, "SELECT ra_temp() AS v", 7)
			for _, parameter := range []string{"role", "session_authorization"} {
				want := proceduresDiagnostic("42501", "cannot set parameter \""+parameter+"\" within security-definer function", 0)
				want.Context = "PL/pgSQL function ra_change_" + parameter + "() line 1 at RETURN"
				attributesQueryError(t, p, b, c, "SELECT ra_change_"+parameter+"()", want)
				attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", "ra_local_caller")
			}
		})
	})
	t.Run("search_path_and_inheritance", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_path_owner", "ra_path_reader", "ra_path_setonly", "ra_path_caller", "ra_path_super"})
		fixture.SchemaReferenceCleanup(t, "ra_trusted")
		fixture.SchemaReferenceCleanup(t, "ra_untrusted")
		fixture.SchemaReferenceCleanup(t, "ra_path_owner")
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_path_owner", "CREATE ROLE ra_path_reader", "CREATE ROLE ra_path_setonly", "CREATE ROLE ra_path_caller", "CREATE ROLE ra_path_super SUPERUSER",
				"GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE SCHEMA ra_trusted", "CREATE SCHEMA ra_untrusted", "CREATE SCHEMA ra_path_owner AUTHORIZATION ra_path_owner",
				"GRANT USAGE ON SCHEMA ra_trusted,ra_untrusted TO PUBLIC", "CREATE TABLE ra_trusted.ra_path(v integer)", "CREATE TABLE ra_untrusted.ra_path(v integer)", "CREATE TABLE ra_path_owner.ra_path(v integer)",
				"ALTER TABLE ra_path_owner.ra_path OWNER TO ra_path_owner",
				"GRANT SELECT ON ra_trusted.ra_path,ra_untrusted.ra_path TO PUBLIC")
			for _, item := range []struct {
				schema string
				value  int
			}{{"ra_trusted", 42}, {"ra_untrusted", 9}, {"ra_path_owner", 73}} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+item.schema+".ra_path VALUES($1)", 1, item.value))
			}
			resultsExec(t, p, b, c,
				"SET search_path=ra_untrusted,public",
				"CREATE FUNCTION public.ra_path_plain() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS 'SELECT v FROM ra_path'",
				"CREATE FUNCTION public.ra_path_safe() RETURNS integer LANGUAGE SQL SECURITY DEFINER SET search_path=ra_trusted,pg_temp AS 'SELECT v FROM ra_path'",
				"SET search_path=public",
				"ALTER FUNCTION ra_path_plain() OWNER TO ra_path_owner", "ALTER FUNCTION ra_path_safe() OWNER TO ra_path_owner",
				"GRANT ra_path_reader TO ra_path_owner WITH INHERIT TRUE,SET FALSE",
				"GRANT ra_path_setonly TO ra_path_owner WITH INHERIT FALSE,SET TRUE",
				"GRANT ra_path_super TO ra_path_owner WITH INHERIT TRUE",
				"CREATE FUNCTION ra_super_attribute() RETURNS text LANGUAGE SQL SECURITY DEFINER AS $$SELECT current_setting('is_superuser')$$",
				"ALTER FUNCTION ra_super_attribute() OWNER TO ra_path_owner",
				"CREATE TABLE ra_inherited(v integer)", "GRANT SELECT ON ra_inherited TO ra_path_reader")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_inherited VALUES(42)", 1))
			resultsExec(t, p, b, c, "CREATE FUNCTION ra_inherited_read() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS 'SELECT v FROM public.ra_inherited'", "ALTER FUNCTION ra_inherited_read() OWNER TO ra_path_owner",
				"SET SESSION AUTHORIZATION ra_path_caller", "SET search_path=ra_untrusted,public")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			attributesIntBoth(t, p, b, c, "SELECT public.ra_path_plain() AS v", 9)
			attributesIntBoth(t, p, b, c, "SELECT public.ra_path_safe() AS v", 42)
			resultsExec(t, p, b, c, "SET search_path='$user',public")
			attributesIntBoth(t, p, b, c, "SELECT public.ra_path_plain() AS v", 73)
			attributesIntBoth(t, p, b, c, "SELECT public.ra_inherited_read() AS v", 42)
			attributesTextBoth(t, p, b, c, "SELECT public.ra_super_attribute() AS v", "off")
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON ra_inherited FROM ra_path_reader", "GRANT SELECT ON ra_inherited TO ra_path_setonly", "SET SESSION AUTHORIZATION ra_path_caller")
			want := proceduresDiagnostic("42501", "permission denied for table ra_inherited", 0)
			want.Context = `SQL function "ra_inherited_read" statement 1`
			attributesQueryError(t, p, b, c, "SELECT public.ra_inherited_read()", want)
		})
	})
}
