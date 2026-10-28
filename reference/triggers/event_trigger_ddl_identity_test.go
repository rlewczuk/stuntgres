package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLIdentity(t *testing.T) {
	eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
		eventTriggerScenario(t, []string{"DDL old", "DDL new", "et_ddl_taken"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, route}
			d.seed(`"DDL old"`, "ddl_command_end", "'ALTER TABLE'")
			d.utility(`ALTER EVENT TRIGGER "DDL old" ENABLE ALWAYS`)
			i := d.identity("DDL old", "ddl_command_end", "A", `{"ALTER TABLE"}`)
			q := "SELECT oid,evtname,evtevent,evtowner,evtfoid,evtenabled,evttags FROM pg_event_trigger ORDER BY evtname"
			h := d.held(q)
			defer h.Statement.Close()
			oldMutation := d.held(`ALTER EVENT TRIGGER "DDL old" DISABLE`)
			defer oldMutation.Statement.Close()
			eligible := d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
			defer eligible.Statement.Close()
			d.audit()
			d.utility(`ALTER EVENT TRIGGER "DDL old" RENAME TO "DDL new"`)
			i.name = "DDL new"
			d.catalog(h, i)
			d.dependencies(i, true)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='DDL old'", 0))
			s := fixture.ErrorDiagnostic(oldMutation.SQL, eventTriggerDiagnostic("42704", `event trigger "DDL old" does not exist`))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, oldMutation, b.Recorder, s)
			d.catalog(c, i)
			d.failure(`ALTER EVENT TRIGGER "DDL new" RENAME TO "DDL new"`, eventTriggerDiagnostic("42710", `event trigger "DDL new" already exists`), false)
			d.catalog(c, i)
			d.audit()
			d.heldExec(eligible)
			d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
			d.clearAudit(1)
			eventTriggerCreate(t, p, b, c, "et_ddl_taken", "ddl_command_end", "'CREATE TABLE'", "et_ddl_fn")
			d.failure(`ALTER EVENT TRIGGER "DDL new" RENAME TO et_ddl_taken`, eventTriggerDiagnostic("42710", `event trigger "et_ddl_taken" already exists`), false)
			d.utility("DROP EVENT TRIGGER et_ddl_taken")
			d.catalog(h, i)
			d.audit()
			// A newly created object with the old spelling is a distinct identity;
			// held catalog reads still identify the renamed predecessor exactly.
			eventTriggerCreate(t, p, b, c, `"DDL old"`, "ddl_command_start", "'CREATE TABLE'", "et_ddl_fn")
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='DDL old' AND oid<>"+fmt.Sprint(i.oid), 1))
			d.utility(`DROP EVENT TRIGGER "DDL old"`)
			d.catalog(h, i)
		})
	})
	t.Run("actual_alphabetical_order", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_ddl_a", "et_ddl_z", "et_ddl_0"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			eventTriggerAuditSetup(t, p, b, c)
			d.exec("CREATE TABLE et_ddl_counter(n integer)", "CREATE TABLE et_ddl_data(id integer)")
			d.run(fixture.Exec("INSERT INTO et_ddl_counter VALUES(0)", 1))
			for _, label := range []string{"a", "z"} {
				d.exec("CREATE FUNCTION et_ddl_order_" + label + "() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN UPDATE public.et_ddl_counter SET n=n+1; INSERT INTO public.et_audit SELECT n,'body_" + label + "',TG_EVENT,TG_TAG FROM public.et_ddl_counter; END$$")
			}
			for _, label := range []string{"a", "z"} {
				eventTriggerCreate(t, p, b, c, "et_ddl_"+label, "ddl_command_end", "'ALTER TABLE'", "et_ddl_order_"+label)
			}
			h := d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
			defer h.Statement.Close()
			d.heldExec(h)
			d.audit(eventTriggerRow(1, "body_a", "ddl_command_end", "ALTER TABLE"), eventTriggerRow(2, "body_z", "ddl_command_end", "ALTER TABLE"))
			d.clearAudit(2)
			d.utility("ALTER EVENT TRIGGER et_ddl_z RENAME TO et_ddl_0")
			d.heldExec(h)
			d.audit(eventTriggerRow(3, "body_z", "ddl_command_end", "ALTER TABLE"), eventTriggerRow(4, "body_a", "ddl_command_end", "ALTER TABLE"))
		})
	})
}
