package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_smoke_role"})
	eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		eventTriggerAuditSetup(t, p, b, c)
		for i, name := range eventTriggerPhaseNames {
			eventTriggerFunction(t, p, b, c, name, i+1, "RETURN;")
		}
		// Deliberately install the final event before the start event.
		eventTriggerCreate(t, p, b, c, "et_phase_4", "ddl_command_end", "'CREATE TABLE','ALTER TABLE','DROP TABLE'", "et_phase_4")
		eventTriggerCreate(t, p, b, c, "et_phase_3", "table_rewrite", "'ALTER TABLE'", "et_phase_3")
		eventTriggerCreate(t, p, b, c, "et_phase_2", "sql_drop", "'DROP TABLE','ALTER TABLE'", "et_phase_2")
		eventTriggerCreate(t, p, b, c, "et_phase_1", "ddl_command_start", "'CREATE TABLE','ALTER TABLE','DROP TABLE'", "et_phase_1")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		phases := func(tag string, drop, rewrite bool) fixture.Step {
			rows := [][]any{eventTriggerRow(1, "et_phase_1", "ddl_command_start", tag)}
			if drop {
				rows = append(rows, eventTriggerRow(2, "et_phase_2", "sql_drop", tag))
			}
			if rewrite {
				rows = append(rows, eventTriggerRow(3, "et_phase_3", "table_rewrite", tag))
			}
			return eventTriggerAudit(append(rows, eventTriggerRow(4, "et_phase_4", "ddl_command_end", tag))...)
		}
		clear := func(n int64) { run(fixture.Exec("DELETE FROM et_audit", n)) }
		run(fixture.Exec("CREATE TABLE et_smoke(id integer NOT NULL)", 0))
		run(phases("CREATE TABLE", false, false))
		clear(2)
		q := "ALTER TABLE et_smoke ALTER COLUMN id SET DEFAULT 7"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		held := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, rec, s) }
		run(eventTriggerAudit())
		held(fixture.Exec(q, 0))
		run(phases("ALTER TABLE", false, false))
		clear(2)
		for _, q := range []string{"ALTER EVENT TRIGGER et_phase_1 DISABLE", "ALTER EVENT TRIGGER et_phase_4 DISABLE"} {
			run(fixture.Exec(q, 0))
		}
		held(fixture.Exec(q, 0))
		run(eventTriggerAudit())
		eventTriggerFunction(t, p, b, c, "et_phase_1", 8, "RETURN;")
		for _, q := range []string{"ALTER EVENT TRIGGER et_phase_1 ENABLE", "ALTER EVENT TRIGGER et_phase_4 ENABLE"} {
			run(fixture.Exec(q, 0))
		}
		held(fixture.Exec(q, 0))
		run(eventTriggerAudit(eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE"), eventTriggerRow(8, "et_phase_1", "ddl_command_start", "ALTER TABLE")))
		clear(2)
		eventTriggerFunction(t, p, b, c, "et_phase_1", 1, "RETURN;")
		run(fixture.Exec("INSERT INTO et_smoke DEFAULT VALUES", 1))
		run(fixture.Prepared(fixture.Exec("ALTER TABLE et_smoke ALTER COLUMN id TYPE bigint", 0)))
		run(phases("ALTER TABLE", false, true))
		run(fixture.Select("SELECT id FROM et_smoke ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}}))
		clear(3)
		eventTriggerFunction(t, p, b, c, "et_phase_4", 4, "RAISE EXCEPTION 'event end blocked';")
		run(fixture.Exec("INSERT INTO et_audit VALUES(99,'sentinel','committed','none')", 1))
		run(fixture.ErrorDiagnostic("CREATE TABLE et_smoke_rejected(id integer)", eventTriggerFailure("et_phase_4", "event end blocked")))
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_smoke_rejected'", 0))
		run(eventTriggerAudit(eventTriggerRow(99, "sentinel", "committed", "none")))
		clear(1)
		eventTriggerFunction(t, p, b, c, "et_phase_4", 4, "RETURN;")
		eventTriggerFunction(t, p, b, c, "et_phase_1", 1, "RAISE NOTICE '%',TG_TAG; RETURN;")
		notices = nil
		run(fixture.ErrorDiagnostic("DROP TABLE et_smoke_absent", eventTriggerDiagnostic("42P01", `table "et_smoke_absent" does not exist`)))
		fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{eventTriggerNotice("et_phase_1", "DROP TABLE")})
		run(eventTriggerAudit())
		eventTriggerFunction(t, p, b, c, "et_phase_1", 1, "RETURN;")
		run(fixture.Exec("DROP TABLE et_smoke", 0))
		run(phases("DROP TABLE", true, false))
		clear(3)
		run(fixture.Exec("DROP TABLE IF EXISTS et_smoke", 0))
		run(phases("DROP TABLE", false, false))
		clear(2)
		for _, q := range []string{"ALTER EVENT TRIGGER et_phase_1 DISABLE", "ALTER EVENT TRIGGER et_phase_1 ENABLE", "CREATE ROLE et_smoke_role", "DROP ROLE et_smoke_role"} {
			run(fixture.Exec(q, 0))
		}
		run(eventTriggerAudit())
		check := func(conn *sql.Conn, backend fixture.Backend) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			r(eventTriggerAudit())
			r(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname IN ('et_phase_1','et_phase_2','et_phase_3','et_phase_4') AND evtenabled='O'", 4))
			r(fixture.Exec("BEGIN", 0))
			r(fixture.Exec("CREATE TABLE et_after_smoke_restore(id integer)", 0))
			r(phases("CREATE TABLE", false, false))
			r(fixture.Exec("ROLLBACK", 0))
		}
		check(c, b)
		if !b.PG {
			for range 2 {
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
				check(c, b)
			}
		}
	})
}
