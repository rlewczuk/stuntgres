package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerSettingPhaseChanges(t *testing.T) {
	for _, command := range []string{"ALTER TABLE et_dynamic ALTER COLUMN v TYPE bigint", "DROP TABLE et_dynamic"} {
		t.Run(command, func(t *testing.T) {
			eventTriggerScenario(t, []string{"et_a_start", "et_z_start", "et_drop", "et_rewrite", "et_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
				d.exec("CREATE TABLE et_dynamic(v integer)")
				d.run(fixture.Exec("INSERT INTO et_dynamic VALUES(7)", 1))
				eventTriggerAuditSetup(t, p, b, c)
				for _, phase := range []struct{ name, event, body string; seq int }{
					{"et_a_start", "ddl_command_start", "PERFORM set_config('event_triggers','off',false); RETURN;", 1},
					{"et_z_start", "ddl_command_start", "RETURN;", 2},
					{"et_drop", "sql_drop", "RETURN;", 3},
					{"et_rewrite", "table_rewrite", "RETURN;", 4},
					{"et_end", "ddl_command_end", "RETURN;", 5},
				} {
					eventTriggerFunction(t, p, b, c, phase.name, phase.seq, phase.body)
					tags := "'ALTER TABLE','DROP TABLE'"
					if phase.event == "table_rewrite" { tags = "'ALTER TABLE'" }
					eventTriggerCreate(t, p, b, c, phase.name, phase.event, tags, phase.name)
				}
				d.utility(command)
				tag := "ALTER TABLE"
				if command == "DROP TABLE et_dynamic" { tag = "DROP TABLE" }
				d.audit(eventTriggerRow(1, "et_a_start", "ddl_command_start", tag), eventTriggerRow(2, "et_z_start", "ddl_command_start", tag))
				eventSettingShow(d, "off")
				if tag == "ALTER TABLE" {
					d.run(fixture.Select("SELECT v FROM et_dynamic", []string{"v"}, []string{"INT8"}, [][]any{{int64(7)}}))
				} else { eventSettingTarget(d, "et_dynamic", 0) }
			})
		})
	}
	t.Run("drop_captured_list", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_start", "et_a_drop", "et_z_drop", "et_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			d.exec("CREATE TABLE et_dynamic(v integer)")
			eventTriggerAuditSetup(t, p, b, c)
			for _, phase := range []struct{ name, event, body string; seq int }{
				{"et_start", "ddl_command_start", "RETURN;", 1},
				{"et_a_drop", "sql_drop", "PERFORM set_config('event_triggers','off',false); RETURN;", 2},
				{"et_z_drop", "sql_drop", "RETURN;", 3},
				{"et_end", "ddl_command_end", "RETURN;", 4},
			} {
				eventTriggerFunction(t, p, b, c, phase.name, phase.seq, phase.body)
				eventTriggerCreate(t, p, b, c, phase.name, phase.event, "'DROP TABLE'", phase.name)
			}
			d.utility("DROP TABLE et_dynamic")
			d.audit(eventTriggerRow(1, "et_start", "ddl_command_start", "DROP TABLE"), eventTriggerRow(2, "et_a_drop", "sql_drop", "DROP TABLE"), eventTriggerRow(3, "et_z_drop", "sql_drop", "DROP TABLE"))
			eventSettingShow(d, "off")
			eventSettingTarget(d, "et_dynamic", 0)
		})
	})
	t.Run("routine_local_nested", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_a_start", "et_z_start", "et_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			eventTriggerAuditSetup(t, p, b, c)
			d.exec("CREATE FUNCTION et_a_start() RETURNS event_trigger LANGUAGE plpgsql SET event_triggers=off AS $$DECLARE n integer; BEGIN SELECT public.et_record(1,'et_a_start',TG_EVENT,TG_TAG) INTO n; CREATE TABLE public.et_nested(v integer); END$$")
			eventTriggerFunction(t, p, b, c, "et_z_start", 2, "RETURN;")
			eventTriggerFunction(t, p, b, c, "et_end", 3, "RETURN;")
			eventTriggerCreate(t, p, b, c, "et_a_start", "ddl_command_start", "'CREATE TABLE'", "et_a_start")
			eventTriggerCreate(t, p, b, c, "et_z_start", "ddl_command_start", "'CREATE TABLE'", "et_z_start")
			eventTriggerCreate(t, p, b, c, "et_end", "ddl_command_end", "'CREATE TABLE'", "et_end")
			d.utility("CREATE TABLE et_outer(v integer)")
			d.audit(eventTriggerRow(1, "et_a_start", "ddl_command_start", "CREATE TABLE"), eventTriggerRow(2, "et_z_start", "ddl_command_start", "CREATE TABLE"), eventTriggerRow(3, "et_end", "ddl_command_end", "CREATE TABLE"))
			eventSettingShow(d, "on")
			eventSettingTarget(d, "et_nested", 1)
			eventSettingTarget(d, "et_outer", 1)
		})
	})
	t.Run("initial_off_collection", func(t *testing.T) {
		eventTriggerScenario(t, []string{"et_start", "et_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			eventTriggerAuditSetup(t, p, b, c)
			d.exec(
				"CREATE TABLE et_collected(command_tag text,object_type text,schema_name text,object_identity text,objid oid,present boolean)",
				"CREATE FUNCTION et_enable() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM set_config('event_triggers','on',false); RETURN 7; END$$",
				"CREATE FUNCTION et_end() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_record(4,'et_end',TG_EVENT,TG_TAG) INTO n; INSERT INTO public.et_collected SELECT command_tag,object_type,schema_name,object_identity,objid,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END$$",
			)
			eventTriggerFunction(t, p, b, c, "et_start", 1, "RETURN;")
			eventTriggerCreate(t, p, b, c, "et_start", "ddl_command_start", "'CREATE TABLE AS'", "et_start")
			eventTriggerCreate(t, p, b, c, "et_end", "ddl_command_end", "'CREATE TABLE AS'", "et_end")
			d.exec("SET event_triggers=off")
			d.run(fixture.Exec("CREATE TABLE et_enabled AS SELECT public.et_enable() AS v", 1))
			d.audit(eventTriggerRow(4, "et_end", "ddl_command_end", "CREATE TABLE AS"))
			eventSettingShow(d, "on")
			d.run(fixture.Select("SELECT v FROM et_enabled", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			d.run(fixture.Select("SELECT command_tag,object_type,schema_name,object_identity,present FROM et_collected", []string{"command_tag", "object_type", "schema_name", "object_identity", "present"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"CREATE TABLE AS", "table", "public", "public.et_enabled", true}}))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_collected a JOIN pg_class c ON a.objid=c.oid WHERE c.relnamespace='public'::regnamespace AND c.relname='et_enabled' AND c.oid>0", 1))
		})
	})
}
