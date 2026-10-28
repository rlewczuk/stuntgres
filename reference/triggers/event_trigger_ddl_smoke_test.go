package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLSmoke(t *testing.T) {
	eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
		fixture.RoleReferenceCleanup(t, []string{"et_ddl_smoke_owner"})
		eventTriggerScenario(t, []string{"et_ddl_smoke", "et_ddl_smoke_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = rec
			d := eventDDLCase{t, p, b, c, route}
			d.exec("CREATE ROLE et_ddl_smoke_owner SUPERUSER")
			d.seed("et_ddl_smoke", "ddl_command_end", "'ALTER TABLE'")
			i := d.identity("et_ddl_smoke", "ddl_command_end", "O", `{"ALTER TABLE"}`)
			d.utility("ALTER EVENT TRIGGER et_ddl_smoke RENAME TO et_ddl_smoke_new")
			d.utility("ALTER EVENT TRIGGER et_ddl_smoke_new OWNER TO et_ddl_smoke_owner")
			i.name, i.owner = "et_ddl_smoke_new", d.role("et_ddl_smoke_owner")
			d.catalog(c, i)
			h := d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
			defer h.Statement.Close()
			d.audit()
			d.utility("ALTER EVENT TRIGGER et_ddl_smoke_new ENABLE REPLICA")
			i.mode = "R"
			d.catalog(c, i)
			d.heldExec(h)
			d.audit()
			defer c.ExecContext(context.Background(), "RESET session_replication_role")
			d.exec("SET session_replication_role=replica")
			d.heldExec(h)
			d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
			d.clearAudit(1)
			d.utility("ALTER EVENT TRIGGER et_ddl_smoke_new ENABLE ALWAYS")
			d.exec("SET session_replication_role=origin")
			d.heldExec(h)
			d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
			d.clearAudit(1)
			i.mode = "A"
			d.catalog(c, i)
			if !b.PG {
				h.Statement.Close()
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
				i = d.identity("et_ddl_smoke_new", "ddl_command_end", "A", `{"ALTER TABLE"}`)
				if i.owner != d.role("et_ddl_smoke_owner") {
					t.Fatal("restored owner relationship lost")
				}
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid WHERE e.evtname='et_ddl_smoke_new' AND f.proname='et_ddl_fn'", 1))
				d.audit()
				h = d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
				defer h.Statement.Close()
				d.heldExec(h)
				d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
				d.clearAudit(1)
			}
			d.utility("DROP EVENT TRIGGER et_ddl_smoke_new")
			d.catalog(c)
			d.dependencies(i, false)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc WHERE proname='et_ddl_fn'", 1))
			d.heldExec(h)
			d.audit()
			notices = nil
			d.utility("DROP EVENT TRIGGER IF EXISTS et_ddl_smoke_new")
			fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `event trigger "et_ddl_smoke_new" does not exist, skipping`}})
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='et_ddl_smoke'", 0))
			d.run(fixture.Select("SELECT id FROM et_ddl_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			d.audit()
		})
	})
}
