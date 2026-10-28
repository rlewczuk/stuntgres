package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsOptionsAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"settings_options_reader"})
	settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,owner,b.Recorder,s) }
		run(fixture.Exec(`CREATE ROLE settings_options_reader`,0))
		targets := []string{"pg_catalog.pg_settings_get_flags(text)","pg_catalog.pg_options_to_table(text[])"}
		defer func(){
			if _,err := owner.ExecContext(p.Ctx,`RESET SESSION AUTHORIZATION`); err != nil { t.Error(err); return }
			for _,target := range targets {
				for _,q := range []string{"GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC","REVOKE EXECUTE ON FUNCTION "+target+" FROM settings_options_reader CASCADE"} {
					if _,err := owner.ExecContext(p.Ctx,q); err != nil { t.Error(err) }
				}
			}
		}()
		for _,route := range []string{"direct","fresh","held"} {
			query := `SELECT pg_settings_get_flags($1)`
			flags := settingsLabelRoute(t,p.Ctx,owner,b.Recorder,route,query)
			fixture.RoleGrantActor(run,"settings_options_reader",func(){
				flags(settingsOptionsFlags(b.PG,query,"{}","config_file"))
				flags(settingsOptionsFlags(b.PG,query,"{NO_RESET_ALL,NO_SHOW_ALL,NOT_IN_SAMPLE}","role"))
				s := settingsLabelFailure(`SELECT current_setting('config_file')`,fixture.Diagnostic{Code:"42501",Message:`permission denied to examine "config_file"`,Detail:`Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`})
				s.Prepared = route != "direct"
				run(s)
			})
		}
		for i,tc := range []struct{name,query string; args []any}{
			{"pg_settings_get_flags",`SELECT pg_settings_get_flags($1)`,[]any{"work_mem"}},
			{"pg_options_to_table",`SELECT * FROM pg_options_to_table($1::text[])`,[]any{`{x=1}`}},
		} {
			value := func() fixture.Step {
				if i == 0 { return settingsOptionsFlags(b.PG,tc.query,"{EXPLAIN}",tc.args...) }
				return settingsOptionsRows(b.PG,tc.query,[][]any{{"x","1"}},tc.args...)
			}
			var held fixture.HeldStatement
			fixture.RoleGrantActor(run,"settings_options_reader",func(){
				stmt,err := owner.PrepareContext(p.Ctx,tc.query)
				if err != nil { t.Fatal(err) }
				deferClose := stmt
				t.Cleanup(func(){ deferClose.Close() })
				held = fixture.HeldStatement{Executor:owner,Statement:stmt,SQL:tc.query}
				fixture.RunStep(t,p.Ctx,held,b.Recorder,value())
			})
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+targets[i]+" FROM PUBLIC",0))
			for _,allowed := range []bool{false,true} {
				if allowed { run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets[i]+" TO settings_options_reader",0)) }
				fixture.RoleGrantActor(run,"settings_options_reader",func(){
					for _,route := range []string{"direct","fresh","held"} {
						s := value()
						if !allowed { s = settingsLabelFailure(tc.query,fixture.Diagnostic{Code:"42501",Message:"permission denied for function "+tc.name},tc.args...) }
						s.Prepared = route == "fresh"
						if route == "held" { fixture.RunStep(t,p.Ctx,held,b.Recorder,s) } else { run(s) }
					}
				})
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets[i]+" TO PUBLIC",0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+targets[i]+" FROM settings_options_reader",0))
		}
	})
}
