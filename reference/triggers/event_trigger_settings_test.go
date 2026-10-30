package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func eventSettingShow(d eventDDLCase, value string) {
	d.t.Helper()
	d.run(fixture.Select("SHOW event_triggers", []string{"event_triggers"}, []string{"TEXT"}, [][]any{{value}}))
}

func eventSettingPhases(d eventDDLCase, enabled bool, tag string, phases ...int64) {
	d.t.Helper()
	var rows [][]any
	if enabled {
		for _, phase := range phases {
			event := map[int64]string{1: "ddl_command_start", 2: "sql_drop", 3: "table_rewrite", 4: "ddl_command_end"}[phase]
			rows = append(rows, eventTriggerRow(phase, fmt.Sprintf("et_phase_%d", phase), event, tag))
		}
	}
	d.audit(rows...)
}

func eventSettingTarget(d eventDDLCase, name string, count int64) {
	d.t.Helper()
	d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='"+name+"'", count))
}

func TestReferenceEventTriggerSettingEffects(t *testing.T) {
	for _, value := range []string{"on", "off"} {
		t.Run(value, func(t *testing.T) {
			eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
				eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					d := eventDDLCase{t, p, b, c, route}
					eventTriggerPhases(t, p, b, c, "'CREATE TABLE','ALTER TABLE','DROP TABLE'")
					var identities []eventDDLIdentity
					// Capture only generated IDs; the remaining catalog fields are literal.
					for i, phase := range []string{"ddl_command_start", "sql_drop", "table_rewrite", "ddl_command_end"} {
						var oid, owner, function int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid,evtowner,evtfoid FROM pg_event_trigger WHERE evtname=$1", eventTriggerPhaseNames[i]).Scan(&oid, &owner, &function); err != nil { t.Fatal(err) }
						if oid <= 0 || owner <= 0 || function <= 0 { t.Fatal("invalid trigger identity") }
						tags := any("{\"CREATE TABLE\",\"ALTER TABLE\",\"DROP TABLE\"}")
						if i == 2 { tags = "{\"ALTER TABLE\"}" }
						identities = append(identities, eventDDLIdentity{oid, owner, function, eventTriggerPhaseNames[i], phase, "O", tags})
					}
					d.catalog(c, identities...)
					d.exec("SET event_triggers="+value)
					eventSettingShow(d, value)
					d.utility("CREATE TABLE et_setting_target(v integer)")
					eventSettingPhases(d, value == "on", "CREATE TABLE", 1, 4)
					d.clearAudit(map[bool]int64{true: 2}[value == "on"])
					d.run(fixture.Exec("INSERT INTO et_setting_target VALUES(7)", 1))
					d.run(fixture.Select("SELECT v FROM et_setting_target", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					d.utility("ALTER TABLE et_setting_target ALTER COLUMN v TYPE bigint")
					eventSettingPhases(d, value == "on", "ALTER TABLE", 1, 3, 4)
					d.clearAudit(map[bool]int64{true: 3}[value == "on"])
					d.run(fixture.Select("SELECT v FROM et_setting_target", []string{"v"}, []string{"INT8"}, [][]any{{int64(7)}}))
					d.run(fixture.Select("SELECT t.typname FROM pg_attribute a JOIN pg_class c ON a.attrelid=c.oid JOIN pg_type t ON a.atttypid=t.oid WHERE c.relnamespace='public'::regnamespace AND c.relname='et_setting_target' AND a.attname='v'", []string{"typname"}, []string{"NAME"}, [][]any{{"int8"}}))
					d.utility("DROP TABLE et_setting_target")
					eventSettingPhases(d, value == "on", "DROP TABLE", 1, 2, 4)
					d.clearAudit(map[bool]int64{true: 3}[value == "on"])
					eventSettingTarget(d, "et_setting_target", 0)
					d.run(fixture.ErrorDiagnostic("SET event_triggers='broken'", eventTriggerDiagnostic("22023", `parameter "event_triggers" requires a Boolean value`)))
					eventSettingShow(d, value)
					d.utility("CREATE TABLE et_setting_invalid(v integer)")
					eventSettingPhases(d, value == "on", "CREATE TABLE", 1, 4)
					d.clearAudit(map[bool]int64{true: 2}[value == "on"])
					for i, reset := range []string{"SET event_triggers=DEFAULT", "RESET event_triggers", "RESET ALL"} {
						d.exec("SET event_triggers=off", reset)
						eventSettingShow(d, "on")
						d.utility(fmt.Sprintf("CREATE TABLE et_setting_reset_%d(v integer)", i))
						eventSettingPhases(d, true, "CREATE TABLE", 1, 4)
						d.clearAudit(2)
					}
					d.catalog(c, identities...)
				})
			})
		})
	}
}

func TestReferenceEventTriggerSettingModes(t *testing.T) {
	for _, role := range []string{"origin", "replica"} {
		t.Run(role, func(t *testing.T) {
			names := []string{"et_mode_a_end", "et_mode_a_start", "et_mode_d_end", "et_mode_d_start", "et_mode_o_end", "et_mode_o_start", "et_mode_r_end", "et_mode_r_start"}
			eventTriggerScenario(t, names, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
				eventTriggerAuditSetup(t, p, b, c)
				var modes [][]any
				for _, mode := range []struct{ label, command, stored string }{{"a", "ENABLE ALWAYS", "A"}, {"d", "DISABLE", "D"}, {"o", "ENABLE", "O"}, {"r", "ENABLE REPLICA", "R"}} {
					for i, phase := range []string{"ddl_command_end", "ddl_command_start"} {
						name := "et_mode_"+mode.label+map[int]string{0:"_end", 1:"_start"}[i]
						eventTriggerFunction(t, p, b, c, name, i+1, "RETURN;")
						eventTriggerCreate(t, p, b, c, name, phase, "'CREATE TABLE'", name)
						d.exec("ALTER EVENT TRIGGER "+name+" "+mode.command)
						modes = append(modes, []any{name, mode.stored})
					}
				}
				catalog := fixture.Select("SELECT evtname,evtenabled FROM pg_event_trigger ORDER BY evtname", []string{"evtname", "evtenabled"}, []string{"NAME", "CHAR"}, modes)
				d.run(catalog)
				d.exec("SET session_replication_role="+role)
				for _, value := range []string{"on", "off"} {
					d.exec("SET event_triggers="+value, "CREATE TABLE et_modes_"+value+"(v integer)")
					var rows [][]any
					if value == "on" {
						selected := "o"
						if role == "replica" { selected = "r" }
						for i, phase := range []string{"ddl_command_end", "ddl_command_start"} {
							for _, label := range []string{"a", selected} {
								name := "et_mode_"+label+map[int]string{0:"_end", 1:"_start"}[i]
								rows = append(rows, eventTriggerRow(int64(i+1), name, phase, "CREATE TABLE"))
							}
						}
					}
					d.audit(rows...)
					d.clearAudit(int64(len(rows)))
					d.run(fixture.Exec("INSERT INTO et_modes_"+value+" VALUES(7)", 1))
					d.run(fixture.Select("SELECT v FROM et_modes_"+value, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				d.run(catalog)
			})
		})
	}
}

func TestReferenceEventTriggerSettingPrepared(t *testing.T) {
	eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
		d.exec("CREATE TABLE et_held_setting(v integer)")
		eventTriggerPhases(t, p, b, c, "'ALTER TABLE'")
		held := d.held("ALTER TABLE et_held_setting ALTER COLUMN v SET DEFAULT 7")
		defer held.Statement.Close()
		d.audit()
		for _, value := range []string{"off", "on", "off"} {
			d.exec("SET event_triggers="+value)
			d.heldExec(held)
			eventSettingPhases(d, value == "on", "ALTER TABLE", 1, 4)
			d.clearAudit(map[bool]int64{true:2}[value == "on"])
			d.run(fixture.Exec("INSERT INTO et_held_setting DEFAULT VALUES", 1))
			d.run(fixture.Select("SELECT v FROM et_held_setting", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			d.run(fixture.Exec("DELETE FROM et_held_setting", 1))
		}
		offHeld := d.held("ALTER TABLE et_held_setting ALTER COLUMN v SET DEFAULT 9")
		defer offHeld.Statement.Close()
		d.audit()
		d.exec("SET event_triggers=on")
		d.heldExec(offHeld)
		eventSettingPhases(d, true, "ALTER TABLE", 1, 4)
		d.run(fixture.Exec("INSERT INTO et_held_setting DEFAULT VALUES", 1))
		d.run(fixture.Select("SELECT v FROM et_held_setting", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
	})
}
