package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerTiming(t *testing.T) {
	eventTriggerScenario(t, []string{"et_timing_start", "et_timing_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		for _, q := range []string{
			"CREATE FUNCTION et_timing_start() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM pg_class WHERE relname='et_timing_data'; RAISE NOTICE 'start:%:%',TG_TAG,n; END$$",
			"CREATE FUNCTION et_timing_end() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM pg_class WHERE relname='et_timing_data'; RAISE NOTICE 'end:%:%',TG_TAG,n; END$$",
		} {
			run(fixture.Exec(q, 0))
		}
		eventTriggerCreate(t, p, b, c, "et_timing_start", "ddl_command_start", "'CREATE TABLE','DROP TABLE','ALTER TABLE'", "et_timing_start")
		eventTriggerCreate(t, p, b, c, "et_timing_end", "ddl_command_end", "'CREATE TABLE','DROP TABLE','ALTER TABLE'", "et_timing_end")
		q := "CREATE TABLE et_timing_data(id integer)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(notices) != 0 {
			t.Fatal("Prepare fired callbacks", notices)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, rec, fixture.Exec(q, 0))
		want := []fixture.Diagnostic{eventTriggerNotice("et_timing_start", "start:CREATE TABLE:0"), eventTriggerNotice("et_timing_end", "end:CREATE TABLE:1")}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("timing got %#v want %#v", notices, want)
		}
		for _, q := range []string{"DROP TABLE et_missing", "ALTER TABLE et_missing ADD COLUMN v integer"} {
			notices = nil
			tag, message := "DROP TABLE", `table "et_missing" does not exist`
			if q[0] == 'A' {
				tag, message = "ALTER TABLE", `relation "et_missing" does not exist`
			}
			run(fixture.ErrorDiagnostic(q, eventTriggerDiagnostic("42P01", message)))
			want = []fixture.Diagnostic{eventTriggerNotice("et_timing_start", "start:"+tag+":1")}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("missing object timing got %#v want %#v", notices, want)
			}
		}
		// Start catalog changes are visible to the actual command's resolver.
		for _, q := range []string{
			"ALTER EVENT TRIGGER et_timing_start DISABLE", "ALTER EVENT TRIGGER et_timing_end DISABLE",
			"CREATE FUNCTION et_make_source() RETURNS integer LANGUAGE SQL AS $$CREATE TABLE et_source(id integer); SELECT 1$$",
			"CREATE OR REPLACE FUNCTION et_timing_start() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_make_source() INTO n; END$$",
			"DROP EVENT TRIGGER et_timing_start",
			"CREATE EVENT TRIGGER et_timing_start ON ddl_command_start WHEN TAG IN ('CREATE VIEW') EXECUTE FUNCTION et_timing_start()",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE VIEW et_start_visible AS SELECT id FROM et_source", 0))
		run(fixture.Select("SELECT id FROM et_start_visible", []string{"id"}, []string{"INT4"}, nil))
	})
}

func TestReferenceEventTriggerAtomicity(t *testing.T) {
	for _, phase := range []string{"start", "end"} {
		t.Run(phase+"_failure", func(t *testing.T) {
			eventTriggerScenario(t, []string{"et_fail", "et_later"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				eventTriggerAuditSetup(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("INSERT INTO et_audit VALUES(99,'sentinel','committed','none')", 1))
				eventTriggerFunction(t, p, b, c, "et_fail_fn", 1, "RAISE EXCEPTION 'event "+phase+" blocked';")
				eventTriggerFunction(t, p, b, c, "et_later_fn", 2, "RAISE EXCEPTION 'later callback ran';")
				event := "ddl_command_" + phase
				eventTriggerCreate(t, p, b, c, "et_fail", event, "'CREATE TABLE'", "et_fail_fn")
				eventTriggerCreate(t, p, b, c, "et_later", event, "'CREATE TABLE'", "et_later_fn")
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE TABLE et_rejected(id integer)", eventTriggerFailure("et_fail_fn", "event "+phase+" blocked"))
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_rejected'", 0))
					run(eventTriggerAudit(eventTriggerRow(99, "sentinel", "committed", "none")))
				}
			})
		})
	}
	t.Run("private_until_end", eventTriggerPrivateUntilEnd)
}

func eventTriggerPrivateUntilEnd(t *testing.T) {
	eventTriggerScenario(t, []string{"et_barrier"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		eventTriggerAuditSetup(t, p, b, c)
		for _, q := range []string{
			"CREATE TABLE et_gate(id integer)",
			"CREATE FUNCTION et_barrier_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT count(*) INTO n FROM pg_class WHERE relname='et_private'; IF n<>1 THEN RAISE EXCEPTION 'end missing catalog'; END IF; SELECT count(*) INTO n FROM et_private; SELECT et_record(1,'end',TG_EVENT,TG_TAG) INTO n; SELECT count(*) INTO n FROM et_gate; RAISE EXCEPTION 'event end blocked'; END$$",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		eventTriggerCreate(t, p, b, c, "et_barrier", "ddl_command_end", "'CREATE TABLE'", "et_barrier_fn")
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		defer holder.ExecContext(context.Background(), "ROLLBACK")
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		waiterPID, holderPID := 0, 0
		var oid int64
		if b.PG {
			waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
			oid = fixture.LockOID(t, p.Ctx, "et_gate")
		}
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE et_gate IN ACCESS EXCLUSIVE MODE", 0)
		done := make(chan struct{})
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(done)
			r, err := c.ExecContext(ctx, "CREATE TABLE et_private(id integer)")
			return fixture.RelationOutcome{Result: r, Err: err}
		})
		fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "AccessShareLock", done)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_private'", 0))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, eventTriggerAudit())
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		out := await()
		want := eventTriggerFailure("et_barrier_fn", "event end blocked")
		fixture.AssertDiagnostic(t, out.Err, &want)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_private'", 0))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, eventTriggerAudit())
	})
}

func TestReferenceEventTriggerDrop(t *testing.T) {
	t.Run("callback_failure", eventTriggerDropFailure)
	eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TABLE et_drop(id integer DEFAULT 7,removed text)", "CREATE VIEW et_dependent AS SELECT id FROM et_drop"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		eventTriggerPhases(t, p, b, c, "'DROP TABLE','ALTER TABLE'")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE OR REPLACE FUNCTION et_phase_2() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN IF TG_TAG='DROP TABLE' THEN SELECT count(*) INTO n FROM pg_class WHERE relname IN ('et_drop','et_dependent'); IF n<>0 THEN RAISE EXCEPTION 'drop still visible'; END IF; END IF; SELECT et_record(2,'et_phase_2',TG_EVENT,TG_TAG) INTO n; END$$", 0))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("ALTER TABLE et_drop DROP COLUMN removed", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "ALTER TABLE"), eventTriggerRow(2, "et_phase_2", "sql_drop", "ALTER TABLE"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE")))
		run(fixture.Exec("DELETE FROM et_audit", 3))
		run(fixture.Exec("ALTER TABLE et_drop ALTER COLUMN id DROP DEFAULT", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "ALTER TABLE"), eventTriggerRow(2, "et_phase_2", "sql_drop", "ALTER TABLE"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE")))
		run(fixture.Exec("DELETE FROM et_audit", 3))
		run(fixture.Prepared(fixture.Exec("DROP TABLE et_drop CASCADE", 0)))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "DROP TABLE"), eventTriggerRow(2, "et_phase_2", "sql_drop", "DROP TABLE"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "DROP TABLE")))
		run(fixture.Exec("DELETE FROM et_audit", 3))
		run(fixture.Exec("DROP TABLE IF EXISTS et_drop", 0))
		run(eventTriggerAudit(eventTriggerRow(1, "et_phase_1", "ddl_command_start", "DROP TABLE"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "DROP TABLE")))
		run(fixture.Exec("DELETE FROM et_audit", 2))
		// A later command in the same transaction must not inherit old drops.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP TABLE IF EXISTS et_drop", 0))
		run(fixture.Exec("ALTER TABLE et_audit ADD COLUMN optional integer", 0))
		run(eventTriggerCount("SELECT count(*) AS n FROM et_audit WHERE event='sql_drop'", 0))
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceEventTriggerIdentityDrop(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE et_identity(id integer GENERATED ALWAYS AS IDENTITY)", 0))
				run(fixture.Exec("INSERT INTO et_identity OVERRIDING SYSTEM VALUE VALUES(7)", 1))
				eventTriggerPhases(t, p, b, c, "'ALTER TABLE'")
				step := fixture.Exec("ALTER TABLE et_identity ALTER COLUMN id DROP IDENTITY", 0)
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				run(eventTriggerAudit(
					eventTriggerRow(1, "et_phase_1", "ddl_command_start", "ALTER TABLE"),
					eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE"),
				))
				run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_identity_id_seq'", 0))
				run(fixture.Select("SELECT id FROM et_identity ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.Exec("INSERT INTO et_identity VALUES(9)", 1))
				run(fixture.Select("SELECT id FROM et_identity ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
			})
		})
	}
}

func TestReferenceEventTriggerRewrite(t *testing.T) {
	t.Run("add_column_reasons", eventTriggerAddColumnRewrite)
	t.Run("metadata_first", eventTriggerQueuedRewrite)
	for _, tc := range []struct {
		name, definition, action, typ string
		rows                          [][]any
		rewrite                       bool
	}{
		{"int_widen", "integer", "TYPE bigint", "INT8", [][]any{{int64(1)}, {int64(7)}}, true},
		{"int_widen_empty", "integer", "TYPE bigint", "INT8", nil, true},
		{"using_same_var", "integer", "TYPE integer USING v", "INT4", [][]any{{int64(1)}, {int64(7)}}, false},
		{"using_noop_cast", "integer", "TYPE integer USING v::integer", "INT4", [][]any{{int64(1)}, {int64(7)}}, false},
		{"using_arithmetic", "integer", "TYPE integer USING v+0", "INT4", [][]any{{int64(1)}, {int64(7)}}, true},
		{"text_varchar", "text", "TYPE varchar", "VARCHAR", [][]any{{"1"}, {"7"}}, false},
		{"varchar_text", "varchar(8)", "TYPE text", "TEXT", [][]any{{"1"}, {"7"}}, false},
		{"default_only", "integer", "SET DEFAULT 9", "INT4", [][]any{{int64(1)}, {int64(7)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_rewrite(v "+tc.definition+")", 0))
				if len(tc.rows) != 0 {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_rewrite VALUES(1),(7)", 2))
				}
				eventTriggerPhases(t, p, b, c, "'ALTER TABLE'")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Prepared(fixture.Exec("ALTER TABLE et_rewrite ALTER COLUMN v "+tc.action, 0)))
				rows := [][]any{eventTriggerRow(1, "et_phase_1", "ddl_command_start", "ALTER TABLE"), eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE")}
				if tc.rewrite {
					rows = append(rows[:1], eventTriggerRow(3, "et_phase_3", "table_rewrite", "ALTER TABLE"), rows[1])
				}
				run(eventTriggerAudit(rows...))
				run(fixture.Select("SELECT v FROM et_rewrite ORDER BY v", []string{"v"}, []string{tc.typ}, tc.rows))
			})
		})
	}
	t.Run("shape_and_failure", func(t *testing.T) {
		eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_rewrite(v integer)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_rewrite VALUES(7)", 1))
			eventTriggerPhases(t, p, b, c, "'ALTER TABLE'")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"ALTER TABLE et_rewrite ADD COLUMN extra text", "ALTER TABLE et_rewrite ADD COLUMN constant integer DEFAULT 5", "ALTER TABLE et_rewrite RENAME COLUMN extra TO renamed"} {
				run(fixture.Exec(q, 0))
			}
			run(eventTriggerCount("SELECT count(*) AS n FROM et_audit WHERE event='table_rewrite'", 0))
			run(fixture.Exec("DELETE FROM et_audit", 6))
			run(fixture.Exec("CREATE OR REPLACE FUNCTION et_phase_3() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'rewrite blocked'; END$$", 0))
			run(fixture.ErrorDiagnostic("ALTER TABLE et_rewrite ALTER COLUMN v TYPE bigint", eventTriggerFailure("et_phase_3", "rewrite blocked")))
			run(fixture.Select("SELECT v FROM et_rewrite", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(eventTriggerAudit())
		})
	})
}
