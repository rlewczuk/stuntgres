package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_reader"})
	eventTriggerScenario(t, []string{"et_denied", "et_owned"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE et_reader", "CREATE FUNCTION et_empty() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", "GRANT CREATE ON SCHEMA public TO et_reader"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION et_reader", 0))
		for _, q := range []string{
			"CREATE OR REPLACE FUNCTION public.et_empty() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
			"CREATE OR REPLACE FUNCTION public.et_empty() RETURNS event_trigger LANGUAGE SQL AS $$SELECT 1$$",
			"CREATE OR REPLACE FUNCTION public.et_empty() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$",
			"CREATE OR REPLACE FUNCTION public.et_empty() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
		} {
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(q, eventTriggerDiagnostic("42501", "must be owner of function et_empty"))
				step.Prepared, step.ErrorStage = prepared, "execute"
				run(step)
			}
		}
		want := eventTriggerDiagnostic("42501", `permission denied to create event trigger "et_denied"`)
		want.Hint = "Must be superuser to create an event trigger."
		for _, q := range []string{
			"CREATE EVENT TRIGGER et_denied ON ddl_command_start EXECUTE FUNCTION missing()",
			"CREATE EVENT TRIGGER et_denied ON bad_event WHEN TAG IN ('nonsense') EXECUTE FUNCTION missing()",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc f JOIN pg_roles r ON r.oid=f.proowner WHERE f.proname='et_empty' AND r.rolname=current_user", 1))
		for _, q := range []string{"ALTER ROLE et_reader SUPERUSER", "SET ROLE et_reader"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE EVENT TRIGGER et_owned ON ddl_command_start EXECUTE FUNCTION public.et_empty()", 0))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_roles r ON r.oid=e.evtowner WHERE e.evtname='et_owned' AND r.rolname='et_reader'", 1))
		for _, q := range []string{"RESET ROLE", "ALTER ROLE et_reader NOSUPERUSER", "SET SESSION AUTHORIZATION et_reader"} {
			run(fixture.Exec(q, 0))
		}
		// Owning an existing event trigger suffices for its bounded state/drop
		// lifecycle; the creation-only superuser check must not be reused.
		run(fixture.Exec("ALTER EVENT TRIGGER et_owned DISABLE", 0))
		run(fixture.Exec("ALTER EVENT TRIGGER et_owned ENABLE", 0))
		run(fixture.Exec("DROP EVENT TRIGGER et_owned", 0))
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "CREATE EVENT TRIGGER et_owned ON ddl_command_start EXECUTE FUNCTION et_empty()", "SET SESSION AUTHORIZATION et_reader"} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"ALTER EVENT TRIGGER et_owned DISABLE", "DROP EVENT TRIGGER et_owned"} {
			run(fixture.ErrorDiagnostic(q, eventTriggerDiagnostic("42501", `must be owner of event trigger et_owned`)))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
	t.Run("definition_errors", eventTriggerDefinitionErrors)
}

func eventTriggerDefinitionErrors(t *testing.T) {
	for _, tc := range []struct {
		name, setup, query string
		want               fixture.Diagnostic
	}{
		{"replace_syntax", "", "CREATE OR REPLACE EVENT TRIGGER et_bad ON ddl_command_start EXECUTE FUNCTION missing()", func() fixture.Diagnostic {
			d := eventTriggerDiagnostic("42601", `syntax error at or near "EVENT"`)
			d.Position = "19"
			return d
		}()},
		{"wrong_result", "CREATE FUNCTION et_wrong() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "CREATE EVENT TRIGGER et_bad ON ddl_command_start EXECUTE FUNCTION et_wrong()", eventTriggerDiagnostic("42P17", "function et_wrong must return type event_trigger")},
		{"missing_function", "", "CREATE EVENT TRIGGER et_bad ON ddl_command_start EXECUTE FUNCTION et_absent()", eventTriggerDiagnostic("42883", "function et_absent() does not exist")},
		{"arguments", "", "CREATE FUNCTION et_bad_arg(x integer) RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", func() fixture.Diagnostic {
			d := eventTriggerDiagnostic("42P13", "event trigger functions cannot have declared arguments")
			d.Context = "compilation of PL/pgSQL function \"et_bad_arg\" near line 1"
			return d
		}()},
		{"sql_language", "", "CREATE FUNCTION et_bad_sql() RETURNS event_trigger LANGUAGE SQL AS $$SELECT 1$$", eventTriggerDiagnostic("42P13", "SQL functions cannot return type event_trigger")},
		{"return_expression", "", "CREATE FUNCTION et_bad_return() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$", func() fixture.Diagnostic {
			d := eventTriggerDiagnostic("42804", "RETURN cannot have a parameter in function returning void")
			d.Position = "90"
			return d
		}()},
		{"missing_state", "", "ALTER EVENT TRIGGER et_absent DISABLE", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`)},
		{"missing_drop", "", "DROP EVENT TRIGGER et_absent", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventTriggerScenario(t, []string{"et_bad"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.setup != "" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.setup, 0))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, tc.want)
					s.Prepared = prepared
					if tc.name == "replace_syntax" && prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		})
	}
}

func TestReferenceEventTriggerAbort(t *testing.T) {
	eventTriggerScenario(t, []string{"et_abort", "et_private_create"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var records []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		eventTriggerAuditSetup(t, p, b, c)
		eventTriggerFunction(t, p, b, c, "et_abort_fn", 1, "RAISE NOTICE '%',TG_TAG; RETURN;")
		eventTriggerCreate(t, p, b, c, "et_abort", "ddl_command_start", "'CREATE TABLE','ALTER TABLE','DROP TABLE'", "et_abort_fn")
		for _, q := range []string{"ALTER EVENT TRIGGER et_abort DISABLE", "DROP EVENT TRIGGER et_abort", "CREATE EVENT TRIGGER et_private_create ON ddl_command_end EXECUTE FUNCTION et_abort_fn()"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(q, 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT evtname,evtenabled FROM pg_event_trigger WHERE evtname IN ('et_abort','et_private_create') ORDER BY evtname", []string{"evtname", "evtenabled"}, []string{"NAME", "CHAR"}, [][]any{{"et_abort", "O"}}))
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT et_recover", 0))
		records = nil
		run(fixture.ErrorDiagnostic("DROP TABLE et_absent", eventTriggerDiagnostic("42P01", `table "et_absent" does not exist`)))
		fixture.RegtypeAssertNotices(t, records, []fixture.Diagnostic{eventTriggerNotice("et_abort_fn", "DROP TABLE")})
		records = nil
		run(fixture.ErrorDiagnostic("CREATE TABLE et_aborted(id integer)", eventTriggerDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		if len(records) != 0 {
			t.Fatal("aborted transaction fired event", records)
		}
		run(fixture.Exec("ROLLBACK TO SAVEPOINT et_recover", 0))
		run(fixture.Exec("CREATE TABLE et_recovered(id integer)", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_abort_fn", "ddl_command_start", "CREATE TABLE")))
		run(fixture.Exec("ROLLBACK", 0))
		run(eventTriggerAudit())
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname IN ('et_aborted','et_recovered')", 0))
	})
	t.Run("protected_nested", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_protected"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE FUNCTION et_make_bad() RETURNS integer LANGUAGE SQL AS $$CREATE TABLE et_bad_nested(id integer); SELECT 1$$",
				"CREATE FUNCTION et_make_good() RETURNS integer LANGUAGE SQL AS $$CREATE TABLE et_good_nested(id integer); SELECT 7$$",
				"CREATE FUNCTION et_protected_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM pg_class WHERE relname='et_bad_nested'; IF n>0 THEN RAISE EXCEPTION 'protected'; END IF; END$$",
				"CREATE FUNCTION et_protected_call() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN BEGIN SELECT et_make_bad() INTO n; EXCEPTION WHEN raise_exception THEN n:=0; END; SELECT et_make_good() INTO n; RETURN n; END$$",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			eventTriggerCreate(t, p, b, c, "et_protected", "ddl_command_end", "'CREATE TABLE'", "et_protected_fn")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT et_protected_call() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_bad_nested'", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_good_nested'", 1))
		})
	})
}

func TestReferenceEventTriggerPrepared(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_replace_allowed", "et_replace_denied"})
	eventTriggerScenario(t, []string{"et_prepared"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE et_replace_allowed", "CREATE ROLE et_replace_denied",
			"GRANT USAGE ON SCHEMA public TO et_replace_allowed,et_replace_denied",
			"CREATE FUNCTION et_replace(x integer DEFAULT 2) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
			"REVOKE EXECUTE ON FUNCTION et_replace(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION et_replace(integer) TO et_replace_allowed",
			"CREATE TABLE et_replace_before(oid oid,proowner oid)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		defer c.ExecContext(context.Background(), "RESET ROLE")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_replace_before SELECT oid,proowner FROM pg_proc WHERE proname='et_replace'", 1))
		assertACL := func(value int64) {
			t.Helper()
			for _, role := range []string{"et_replace_allowed", "et_replace_denied"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE "+role, 0))
				if role == "et_replace_allowed" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT public.et_replace() AS v", []string{"v"}, []string{"INT4"}, [][]any{{value}}))
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT public.et_replace()", eventTriggerDiagnostic("42501", "permission denied for function et_replace")))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			}
		}
		assertACL(2)
		oldCall, err := c.PrepareContext(p.Ctx, "SELECT et_replace() AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer oldCall.Close()
		replacementCall := fixture.HeldStatement{Executor: c, Statement: oldCall, SQL: "SELECT et_replace() AS v"}
		fixture.RunStep(t, p.Ctx, replacementCall, b.Recorder, fixture.Select("SELECT et_replace() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE OR REPLACE FUNCTION et_replace(x integer DEFAULT 2) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN x+1; END$$", 0))
		for _, tc := range []struct{ query, message string }{
			{"CREATE OR REPLACE FUNCTION et_replace(y integer DEFAULT 2) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", `cannot change name of input parameter "x"`},
			{"CREATE OR REPLACE FUNCTION et_replace(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "cannot remove parameter defaults from existing function"},
			{"CREATE OR REPLACE FUNCTION et_replace(x integer DEFAULT 2) RETURNS text LANGUAGE SQL AS $$SELECT 'bad'$$", "cannot change return type of existing function"},
		} {
			want := eventTriggerDiagnostic("42P13", tc.message)
			want.Hint = "Use DROP FUNCTION et_replace(integer) first."
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		fixture.RunStep(t, p.Ctx, replacementCall, b.Recorder, fixture.Select("SELECT et_replace() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_proc p JOIN et_replace_before b ON p.oid=b.oid WHERE p.proowner=b.proowner", 1))
		assertACL(3)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE OR REPLACE FUNCTION et_replace(x integer DEFAULT 4) RETURNS integer LANGUAGE SQL AS $$SELECT $1+2$$", 0))
		fixture.RunStep(t, p.Ctx, replacementCall, b.Recorder, fixture.Select("SELECT et_replace() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_proc p JOIN et_replace_before b ON p.oid=b.oid WHERE p.proowner=b.proowner", 1))
		assertACL(6)
		eventTriggerAuditSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_prepared_data(id integer NOT NULL)", 0))
		eventTriggerFunction(t, p, b, c, "et_prepared_fn", 1, "RETURN;")
		badReplacement := "CREATE OR REPLACE FUNCTION et_prepared_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$"
		badBody := eventTriggerDiagnostic("42804", "RETURN cannot have a parameter in function returning void")
		badBody.Position = strconv.Itoa(strings.Index(badReplacement, "1;") + 1)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic(badReplacement, badBody)
			step.Prepared, step.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		eventTriggerCreate(t, p, b, c, "et_prepared", "ddl_command_end", "'ALTER TABLE'", "et_prepared_fn")
		q := "ALTER TABLE et_prepared_data ALTER COLUMN id SET DEFAULT 7"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		held := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(eventTriggerAudit())
		held(fixture.Exec(q, 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_prepared_fn", "ddl_command_end", "ALTER TABLE")))
		run(fixture.Exec("DELETE FROM et_audit", 1))
		run(fixture.Prepared(fixture.Select("ALTER EVENT TRIGGER et_prepared DISABLE", []string{}, []string{}, nil)))
		held(fixture.Exec(q, 0))
		run(eventTriggerAudit())
		run(fixture.Exec("INSERT INTO et_replace_before SELECT oid,proowner FROM pg_proc WHERE proname='et_prepared_fn'", 1))
		run(fixture.Exec("CREATE OR REPLACE FUNCTION et_prepared_fn() RETURNS event_trigger LANGUAGE plpgsql STRICT VOLATILE AS $$DECLARE result integer; BEGIN SELECT public.et_record(8,'et_prepared_fn',TG_EVENT,TG_TAG) INTO result; RETURN; END$$", 0))
		attributes := fixture.Select("SELECT proisstrict,provolatile FROM pg_proc WHERE proname='et_prepared_fn'", []string{"proisstrict", "provolatile"}, []string{"BOOL", "CHAR"}, [][]any{{true, "v"}})
		run(attributes)
		run(fixture.Prepared(attributes))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc p JOIN et_replace_before b ON p.oid=b.oid WHERE p.proname='et_prepared_fn' AND p.proowner=b.proowner", 1))
		run(fixture.Exec("ALTER EVENT TRIGGER et_prepared ENABLE", 0))
		held(fixture.Exec(q, 0))
		run(eventTriggerAudit(eventTriggerRow(8, "et_prepared_fn", "ddl_command_end", "ALTER TABLE")))
		run(fixture.Prepared(fixture.Exec("INSERT INTO et_prepared_data DEFAULT VALUES", 1)))
		s := fixture.Select("SELECT id FROM et_prepared_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 {
				t.Fatal(types, err)
			}
			want := reflect.TypeFor[int64]()
			if b.PG {
				want = reflect.TypeFor[int32]()
			}
			if types[0].ScanType() != want {
				t.Fatal("scan type", types[0].ScanType(), want)
			}
			nullable, known := types[0].Nullable()
			if nullable || known == b.PG {
				t.Fatal("nullability", nullable, known)
			}
		}
		run(s)
		run(fixture.Prepared(s))
	})
}
