package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSettingsOptionsLifecycle(t *testing.T) {
	settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
		settingsOptionsHeldLifecycle(t,p,b,owner,peer)
		query := `SELECT * FROM pg_options_to_table($1::text[])`
		run := settingsLabelRoute(t,p.Ctx,owner,b.Recorder,"held",query)
		for _,tc := range []struct{input any; rows [][]any; fails bool}{
			{`{x=1}`,[][]any{{"x","1"}},false},{nil,nil,false},{`{}`,nil,false},
			{`{x=1,NULL}`,nil,true},{`{y=two}`,[][]any{{"y","two"}},false},
		} {
			if tc.fails { run(settingsOptionsNullElement(query,tc.input)) } else { run(settingsOptionsRows(b.PG,query,tc.rows,tc.input)) }
		}
		// Native cancellation is a public adapter boundary, not a synthesized
		// PostgreSQL SQLSTATE. The same physical owner must remain usable.
		if !b.PG {
			cancelled,cancel := context.WithCancel(p.Ctx); cancel()
			rows,err := owner.QueryContext(cancelled,query,`{x=1}`)
			if rows != nil { rows.Close() }
			if !errors.Is(err,context.Canceled) { t.Fatal(err) }
			run(settingsOptionsRows(false,query,[][]any{{"recovered","yes"}},`{recovered=yes}`))
		}
	})
	// Embedding registration has no PostgreSQL API analogue. It is metadata
	// on the real registry, not a session-created custom placeholder.
	p := fixture.NewScenarioWithDriverConfig(t,func(d *stuntgres.Driver) error {
		return d.RegisterSetting(stuntgres.SettingDefinition{Name:"flags.registered",Type:stuntgres.SettingString,Default:"v"})
	})
	for _,b := range p.Backends() {
		if b.PG { continue }
		for _,prepared := range []bool{false,true} {
			s := settingsOptionsFlags(false,`SELECT pg_settings_get_flags('flags.registered')`,"{}")
			s.Prepared = prepared
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		}
	}
}

func settingsOptionsHeldLifecycle(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn) {
	t.Helper()
	query := `SELECT pg_settings_get_flags($1)`
	run := settingsLabelRoute(t,p.Ctx,owner,b.Recorder,"held",query)
	for range 2 { run(settingsOptionsFlags(b.PG,query,nil,"flags_probe.setting")) }
	fixture.RunStep(t,p.Ctx,owner,b.Recorder,settingsLabelFailure(`SELECT current_setting('flags_probe.setting')`,fixture.Diagnostic{Code:"42704",Message:`unrecognized configuration parameter "flags_probe.setting"`}))
	fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Select(`SELECT set_config('flags_probe.setting','v',false)`,[]string{"set_config"},[]string{"TEXT"},[][]any{{"v"}}))
	want := "{NO_SHOW_ALL,NOT_IN_SAMPLE}"
	run(settingsOptionsFlags(b.PG,query,want,"flags_probe.setting"))
	for _,command := range []string{`RESET flags_probe.setting`,`RESET ALL`} {
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Exec(command,0))
		run(settingsOptionsFlags(b.PG,query,want,"flags_probe.setting"))
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Select(`SELECT current_setting('flags_probe.setting')`,[]string{"current_setting"},[]string{"TEXT"},[][]any{{""}}))
	}
	fixture.RunStep(t,p.Ctx,peer,b.Recorder,settingsOptionsFlags(b.PG,query,nil,"flags_probe.setting"))
	fresh,err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer fresh.Close()
	fixture.RunStep(t,p.Ctx,fresh,b.Recorder,settingsOptionsFlags(b.PG,query,nil,"flags_probe.setting"))
	run(settingsOptionsFlags(b.PG,query,want,"flags_probe.setting"))
}

func TestReferenceSettingsOptionsFeatureSmoke(t *testing.T) {
	settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
		for _,route := range []string{"direct","held"} {
			query := `SELECT pg_settings_get_flags($1)`
			run := settingsLabelRoute(t,p.Ctx,owner,b.Recorder,route,query)
			for _,tc := range []struct{name any; want any}{{"enable_seqscan","{EXPLAIN}"},{"config_file","{}"},{"smoke_unknown",nil},{nil,nil}} {
				run(settingsOptionsFlags(b.PG,query,tc.want,tc.name))
			}
		}
		settingsOptionsHeldLifecycle(t,p,b,owner,peer)
		query := `SELECT * FROM pg_options_to_table(ARRAY['x=1','y=two','a=b=c','bare','empty='])`
		want := [][]any{{"x","1"},{"y","two"},{"a","b=c"},{"bare",nil},{"empty",""}}
		for _,route := range []string{"direct","held"} {
			settingsLabelRoute(t,p.Ctx,owner,b.Recorder,route,query)(settingsOptionsRows(b.PG,query,want))
		}
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,settingsOptionsRows(b.PG,`SELECT * FROM pg_options_to_table(NULL::text[])`,nil))
		heldQuery := `SELECT * FROM pg_options_to_table($1::text[])`
		run := settingsLabelRoute(t,p.Ctx,owner,b.Recorder,"held",heldQuery)
		run(settingsOptionsRows(b.PG,heldQuery,[][]any{{"x","1"}},`{x=1}`))
		run(settingsOptionsNullElement(heldQuery,`{x=1,NULL}`))
		run(settingsOptionsRows(b.PG,heldQuery,[][]any{{"y","two"}},`{y=two}`))
	})
}
