package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLDrop(t *testing.T) {
	for _, suffix := range []string{"", " RESTRICT", " CASCADE"} {
		t.Run("direct_drop"+suffix, func(t *testing.T) {
			eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
				eventTriggerScenario(t, []string{"et_ddl_drop", "et_ddl_drop_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var notices []fixture.Diagnostic
					_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					d := eventDDLCase{t, p, b, c, route}
					d.seed("et_ddl_drop", "ddl_command_end", "'ALTER TABLE'")
					i := d.identity("et_ddl_drop", "ddl_command_end", "O", `{"ALTER TABLE"}`)
					d.utility("ALTER EVENT TRIGGER et_ddl_drop RENAME TO et_ddl_drop_new")
					i.name = "et_ddl_drop_new"
					d.failure("DROP FUNCTION et_ddl_fn() RESTRICT", eventDDLFunctionDependency("et_ddl_fn", i.name), false)
					d.catalog(c, i)
					d.dependencies(i, true)
					h := d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
					defer h.Statement.Close()
					d.utility("DROP EVENT TRIGGER et_ddl_drop_new" + suffix)
					d.catalog(c)
					d.dependencies(i, false)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc WHERE proname='et_ddl_fn'", 1))
					d.heldExec(h)
					d.audit()
					d.failure("DROP EVENT TRIGGER et_ddl_drop_new", eventTriggerDiagnostic("42704", `event trigger "et_ddl_drop_new" does not exist`), false)
					notices = nil
					d.utility("DROP EVENT TRIGGER IF EXISTS et_ddl_drop_new" + suffix)
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `event trigger "et_ddl_drop_new" does not exist, skipping`}
					fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{want})
					d.utility("DROP FUNCTION et_ddl_fn() RESTRICT")
					d.audit()
				})
			})
		})
	}
	t.Run("function_cascade", func(t *testing.T) {
		eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
			eventTriggerScenario(t, []string{"et_ddl_drop", "et_ddl_drop_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				var notices []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				b.Recorder = rec
				d := eventDDLCase{t, p, b, c, route}
				d.seed("et_ddl_drop", "ddl_command_end", "'ALTER TABLE'")
				i := d.identity("et_ddl_drop", "ddl_command_end", "O", `{"ALTER TABLE"}`)
				d.utility("ALTER EVENT TRIGGER et_ddl_drop RENAME TO et_ddl_drop_new")
				notices = nil
				d.utility("DROP FUNCTION et_ddl_fn() CASCADE")
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to event trigger et_ddl_drop_new"}})
				d.catalog(c)
				d.dependencies(i, false)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc WHERE proname='et_ddl_fn'", 0))
				d.audit()
			})
		})
	})
	t.Run("own_ddl_is_silent", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_ddl_start", "et_ddl_end", "et_ddl_drop", "et_ddl_rewrite", "et_ddl_subject", "et_ddl_subject_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			eventTriggerAuditSetup(t, p, b, c)
			for n, name := range []string{"start", "end", "drop", "rewrite"} {
				eventTriggerFunction(t, p, b, c, "et_ddl_"+name+"_fn", n+1, "RETURN;")
			}
			for _, phase := range []struct{ name, event string }{{"start", "ddl_command_start"}, {"end", "ddl_command_end"}, {"drop", "sql_drop"}, {"rewrite", "table_rewrite"}} {
				eventTriggerCreate(t, p, b, c, "et_ddl_"+phase.name, phase.event, "", "et_ddl_"+phase.name+"_fn")
			}
			d.utility("CREATE EVENT TRIGGER et_ddl_subject ON ddl_command_end EXECUTE FUNCTION et_ddl_end_fn()")
			for _, q := range []string{"ALTER EVENT TRIGGER et_ddl_subject DISABLE", "ALTER EVENT TRIGGER et_ddl_subject ENABLE REPLICA", "ALTER EVENT TRIGGER et_ddl_subject ENABLE ALWAYS", "ALTER EVENT TRIGGER et_ddl_subject OWNER TO CURRENT_USER", "ALTER EVENT TRIGGER et_ddl_subject RENAME TO et_ddl_subject_new", "DROP EVENT TRIGGER et_ddl_subject_new"} {
				d.utility(q)
				d.audit()
			}
		})
	})
}
