package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLModes(t *testing.T) {
	for _, phase := range []struct{ event, tag, command string }{
		{"ddl_command_start", "CREATE TABLE", "CREATE TABLE et_ddl_created(id integer)"},
		{"ddl_command_end", "CREATE TABLE", "CREATE TABLE et_ddl_created(id integer)"},
		{"sql_drop", "DROP TABLE", "DROP TABLE et_ddl_data"},
		{"table_rewrite", "ALTER TABLE", "ALTER TABLE et_ddl_data ALTER COLUMN id TYPE bigint"},
	} {
		for _, mode := range []struct{ sql, code string }{{"ENABLE", "O"}, {"DISABLE", "D"}, {"ENABLE REPLICA", "R"}, {"ENABLE ALWAYS", "A"}} {
			for _, role := range []string{"origin", "local", "replica"} {
				t.Run(phase.event+"/"+mode.code+"/"+role, func(t *testing.T) {
					eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
						eventTriggerScenario(t, []string{"et_ddl_mode"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							d := eventDDLCase{t, p, b, c, route}
							d.seed("et_ddl_mode", phase.event, "'"+phase.tag+"'")
							if phase.event == "sql_drop" {
								d.exec("CREATE TABLE et_ddl_drops(kind text,identity text)", "CREATE OR REPLACE FUNCTION et_ddl_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_record(1,'et_ddl_fn',TG_EVENT,TG_TAG) INTO n; INSERT INTO public.et_ddl_drops SELECT object_type,object_identity FROM pg_event_trigger_dropped_objects() WHERE object_type='table'; END$$")
							}
							i := d.identity("et_ddl_mode", phase.event, "O", `{"`+phase.tag+`"}`)
							d.utility("ALTER EVENT TRIGGER et_ddl_mode " + mode.sql)
							i.mode = mode.code
							d.catalog(c, i)
							d.audit()
							defer c.ExecContext(context.Background(), "RESET session_replication_role")
							d.exec("SET session_replication_role=" + role)
							d.utility(phase.command)
							fires := mode.code == "A" || mode.code == "R" && role == "replica" || mode.code == "O" && role != "replica"
							if fires {
								d.audit(eventTriggerRow(1, "et_ddl_fn", phase.event, phase.tag))
							} else {
								d.audit()
							}
							if phase.event == "sql_drop" {
								var rows [][]any
								if fires {
									rows = [][]any{{"table", "public.et_ddl_data"}}
								}
								d.run(fixture.Select("SELECT kind,identity FROM et_ddl_drops ORDER BY kind,identity", []string{"kind", "identity"}, []string{"TEXT", "TEXT"}, rows))
							}
							d.utility("ALTER EVENT TRIGGER et_ddl_mode ENABLE")
							i.mode = "O"
							d.catalog(c, i)
						})
					})
				})
			}
		}
	}
	t.Run("held_effective_setting", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_ddl_held"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			d.seed("et_ddl_held", "ddl_command_end", "'ALTER TABLE'")
			h := d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
			defer h.Statement.Close()
			defer c.ExecContext(context.Background(), "RESET session_replication_role")
			d.audit()
			for _, tc := range []struct {
				mode, role string
				fires      bool
			}{
				{"DISABLE", "origin", false}, {"ENABLE REPLICA", "origin", false},
				{"ENABLE REPLICA", "replica", true}, {"ENABLE ALWAYS", "origin", true},
				{"ENABLE", "replica", false}, {"ENABLE", "local", true},
			} {
				d.exec("ALTER EVENT TRIGGER et_ddl_held "+tc.mode, "SET session_replication_role="+tc.role)
				d.heldExec(h)
				if tc.fires {
					d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
					d.clearAudit(1)
				} else {
					d.audit()
				}
			}
			d.exec("ALTER EVENT TRIGGER et_ddl_held ENABLE REPLICA", "SET session_replication_role=origin", "BEGIN", "SET LOCAL session_replication_role=replica", "SAVEPOINT et_ddl_setting", "SET LOCAL session_replication_role=local")
			d.heldExec(h)
			d.audit()
			d.exec("ROLLBACK TO et_ddl_setting")
			d.heldExec(h)
			d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
			d.exec("COMMIT")
			d.clearAudit(1)
			d.heldExec(h)
			d.audit()
			d.run(fixture.Select("SELECT current_setting('session_replication_role') AS role", []string{"role"}, []string{"TEXT"}, [][]any{{"origin"}}))
		})
	})
}
