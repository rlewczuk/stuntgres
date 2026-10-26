package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func eventTriggerOwnedEligibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_object_owner", "et_object_next"})
	eventTriggerScenario(t, append([]string{"et_owned_event"}, eventTriggerPhaseNames...), func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE et_object_owner", "CREATE ROLE et_object_next",
			"CREATE TABLE et_owned_object(id integer)", "ALTER TABLE et_owned_object OWNER TO et_object_owner",
			"CREATE TABLE et_surviving_object(id integer)", "ALTER TABLE et_surviving_object OWNER TO et_object_next",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		eventTriggerPhases(t, p, b, c, "'DROP OWNED'")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("REASSIGN OWNED BY et_object_next TO et_object_owner", 0))
		run(eventTriggerAudit())
		run(fixture.Exec("DROP OWNED BY et_object_owner", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "DROP OWNED"), eventTriggerRow(2, "et_phase_2", "sql_drop", "DROP OWNED"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "DROP OWNED")))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname IN ('et_owned_object','et_surviving_object')", 0))
		run(fixture.Exec("DELETE FROM et_audit", 3))
		run(fixture.Exec("DROP OWNED BY et_object_owner", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "DROP OWNED"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "DROP OWNED")))
		run(fixture.Exec("DELETE FROM et_audit", 2))
		for _, q := range []string{
			"CREATE FUNCTION et_owned_callback() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
			"ALTER ROLE et_object_owner SUPERUSER", "SET ROLE et_object_owner",
			"CREATE EVENT TRIGGER et_owned_event ON ddl_command_start WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION public.et_owned_callback()",
			"RESET ROLE", "ALTER ROLE et_object_owner NOSUPERUSER",
		} {
			run(fixture.Exec(q, 0))
		}
		denied := eventTriggerDiagnostic("42501", `permission denied to change owner of event trigger "et_owned_event"`)
		denied.Hint = "The owner of an event trigger must be a superuser."
		run(fixture.ErrorDiagnostic("REASSIGN OWNED BY et_object_owner TO et_object_next", denied))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_roles r ON r.oid=e.evtowner WHERE e.evtname='et_owned_event' AND r.rolname='et_object_owner'", 1))
		for _, q := range []string{"REASSIGN OWNED BY et_object_owner TO et_object_owner", "ALTER ROLE et_object_next SUPERUSER", "REASSIGN OWNED BY et_object_owner TO et_object_next"} {
			run(fixture.Exec(q, 0))
		}
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_roles r ON r.oid=e.evtowner WHERE e.evtname='et_owned_event' AND r.rolname='et_object_next'", 1))
		run(fixture.Exec("DROP OWNED BY et_object_next", 0))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='et_owned_event'", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "DROP OWNED"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "DROP OWNED")))
	})
}

func eventTriggerAddColumnRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, clause, query, typ string
		rows                     [][]any
		rewrite                  bool
		initial                  string
		drop                     bool
	}{
		{"constant", "ADD COLUMN added integer DEFAULT 5", "SELECT added FROM et_add ORDER BY id", "INT4", [][]any{{int64(5)}, {int64(5)}}, false, "", false},
		{"volatile", "ADD COLUMN added bigint DEFAULT nextval('et_add_seq')", "SELECT added FROM et_add ORDER BY added", "INT8", [][]any{{int64(1)}, {int64(2)}}, true, "", false},
		{"stored_expression", "ALTER COLUMN added SET EXPRESSION AS(id*2)", "SELECT added FROM et_add ORDER BY id", "INT4", [][]any{{int64(2)}, {int64(14)}}, true, ",added integer GENERATED ALWAYS AS(id) STORED", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{"CREATE TABLE et_add(id integer" + tc.initial + ")", "CREATE SEQUENCE et_add_seq"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_add VALUES(1),(7)", 2))
				eventTriggerPhases(t, p, b, c, "'ALTER TABLE'")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE et_add "+tc.clause, 0))
				rows := [][]any{eventTriggerRow(1, "et_phase_1", "ddl_command_start", "ALTER TABLE")}
				if tc.drop {
					rows = append(rows, eventTriggerRow(2, "et_phase_2", "sql_drop", "ALTER TABLE"))
				}
				if tc.rewrite {
					rows = append(rows, eventTriggerRow(3, "et_phase_3", "table_rewrite", "ALTER TABLE"))
				}
				rows = append(rows, eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE"))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit(rows...))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.query, []string{"added"}, []string{tc.typ}, tc.rows))
			})
		})
	}
}

func eventTriggerDropFailure(t *testing.T) {
	eventTriggerScenario(t, []string{"et_drop_fail"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_drop_kept(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_drop_kept VALUES(7)", 1))
		eventTriggerAuditSetup(t, p, b, c)
		eventTriggerFunction(t, p, b, c, "et_drop_fail", 1, "RAISE EXCEPTION 'drop blocked';")
		eventTriggerCreate(t, p, b, c, "et_drop_fail", "sql_drop", "'DROP TABLE'", "et_drop_fail")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("DROP TABLE et_drop_kept", eventTriggerFailure("et_drop_fail", "drop blocked")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM et_drop_kept", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit())
		// Aborting an explicit drop follows the existing transaction rules too.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
		defer c.ExecContext(context.Background(), "ROLLBACK")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("DROP TABLE et_drop_kept", eventTriggerFailure("et_drop_fail", "drop blocked")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM et_drop_kept", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func eventTriggerQueuedRewrite(t *testing.T) {
	for _, typed := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("typed=%v/fail=%v", typed, fail), func(t *testing.T) {
				eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					names := "'et_rewrite_parent','et_rewrite_child'"
					tag := "ALTER TABLE"
					command := "ALTER TABLE et_rewrite_parent ALTER COLUMN v TYPE bigint"
					setup := []string{"CREATE TABLE et_rewrite_parent(v integer)", "CREATE TABLE et_rewrite_child() INHERITS(et_rewrite_parent)"}
					wantMetadata := 2
					if typed {
						names += ", 'et_rewrite_type'"
						tag = "ALTER TYPE"
						command = "ALTER TYPE et_rewrite_type ALTER ATTRIBUTE v TYPE bigint CASCADE"
						setup = []string{"CREATE TYPE et_rewrite_type AS(v integer)", "CREATE TABLE et_rewrite_parent OF et_rewrite_type", "CREATE TABLE et_rewrite_child OF et_rewrite_type"}
						wantMetadata = 3
					}
					for _, q := range setup {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_rewrite_side(v integer)", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_rewrite_side VALUES(42)", 1))
					for _, q := range []string{"CREATE INDEX et_rewrite_parent_ix ON et_rewrite_parent(v)", "CREATE INDEX et_rewrite_child_ix ON et_rewrite_child(v)"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					}
					for _, q := range []string{"INSERT INTO et_rewrite_parent VALUES(1)", "INSERT INTO et_rewrite_child VALUES(7)"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 1))
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION et_make_rewrite_marker() RETURNS integer LANGUAGE SQL AS $$CREATE TABLE et_rewrite_marker(id integer); ALTER TABLE et_rewrite_side ADD COLUMN kept integer DEFAULT 9; SELECT 1$$", 0))
					eventTriggerPhases(t, p, b, c, "'"+tag+"'")
					metadata := fmt.Sprintf("SELECT count(*) INTO n FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace s ON s.oid=c.relnamespace WHERE s.nspname='public' AND c.relname IN (%s) AND a.attname='v' AND a.atttypid=20", names)
					body := fmt.Sprintf("DECLARE n integer; BEGIN %s; IF n<>%d THEN RAISE EXCEPTION 'rewrite metadata not complete'; END IF; SELECT et_record(3,'et_phase_3',TG_EVENT,TG_TAG) INTO n; SELECT count(*) INTO n FROM pg_class WHERE relname='et_rewrite_marker'; IF n=0 THEN SELECT et_make_rewrite_marker() INTO n; END IF;", metadata, wantMetadata)
					if fail {
						body += " RAISE EXCEPTION 'queued rewrite blocked';"
					}
					body += " END"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE OR REPLACE FUNCTION et_phase_3() RETURNS event_trigger LANGUAGE plpgsql AS $$"+body+"$$", 0))
					if fail {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(command, eventTriggerFailure("et_phase_3", "queued rewrite blocked")))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit())
						fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_rewrite_marker'", 0))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relname='et_rewrite_side' AND a.attname='kept'", 0))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM et_rewrite_side", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(command, 0)))
						rows := [][]any{eventTriggerRow(1, "et_phase_1", "ddl_command_start", tag), eventTriggerRow(3, "et_phase_3", "table_rewrite", tag), eventTriggerRow(3, "et_phase_3", "table_rewrite", tag), eventTriggerRow(4, "et_phase_4", "ddl_command_end", tag)}
						if !typed {
							// The unrelated callback ALTER is a genuine nested command;
							// its matching start/end are not suppressed or folded away.
							rows = append([][]any{eventTriggerRow(1, "et_phase_1", "ddl_command_start", tag)}, rows...)
							rows = append(rows, eventTriggerRow(4, "et_phase_4", "ddl_command_end", tag))
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit(rows...))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_rewrite_marker'", 1))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v,kept FROM et_rewrite_side", []string{"v", "kept"}, []string{"INT4", "INT4"}, [][]any{{int64(42), int64(9)}}))
					}
					wantType := "INT8"
					wantOID := 20
					if fail {
						wantType, wantOID = "INT4", 23
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount(fmt.Sprintf("SELECT count(*) AS n FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relname IN (%s) AND a.attname='v' AND a.atttypid=%d", names, wantOID), int64(wantMetadata)))
					for i, name := range []string{"et_rewrite_parent", "et_rewrite_child"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM ONLY "+name, []string{"v"}, []string{wantType}, [][]any{{[]int64{1, 7}[i]}}))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(fmt.Sprintf("SELECT v FROM ONLY %s WHERE v=%d", name, []int64{1, 7}[i]), []string{"v"}, []string{wantType}, [][]any{{[]int64{1, 7}[i]}}))
					}
				})
			})
		}
	}
}
