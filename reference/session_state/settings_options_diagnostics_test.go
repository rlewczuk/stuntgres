package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsOptionsDiagnostics(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct{call,signature string}{
		{`pg_settings_get_flags()`,`pg_settings_get_flags()`},
		{`pg_settings_get_flags('x','y')`,`pg_settings_get_flags(unknown, unknown)`},
		{`pg_settings_get_flags(1)`,`pg_settings_get_flags(integer)`},
		{`pg_settings_get_flags(true)`,`pg_settings_get_flags(boolean)`},
		{`pg_settings_get_flags(ARRAY[1])`,`pg_settings_get_flags(integer[])`},
		{`pg_settings_get_flags(guc => 'work_mem')`,`pg_settings_get_flags(guc => unknown)`},
		{`pg_options_to_table()`,`pg_options_to_table()`},
		{`pg_options_to_table(ARRAY['x=1'],ARRAY['y=2'])`,`pg_options_to_table(text[], text[])`},
		{`pg_options_to_table(1)`,`pg_options_to_table(integer)`},
		{`pg_options_to_table(true)`,`pg_options_to_table(boolean)`},
		{`pg_options_to_table(ARRAY[1])`,`pg_options_to_table(integer[])`},
		{`pg_options_to_table(wrong => ARRAY['x=1'])`,`pg_options_to_table(wrong => text[])`},
		{`pg_catalog.pg_settings_get_flags(1)`,`pg_catalog.pg_settings_get_flags(integer)`},
		{`pg_catalog.pg_options_to_table(1)`,`pg_catalog.pg_options_to_table(integer)`},
		{`public.pg_settings_get_flags('x')`,`public.pg_settings_get_flags(unknown)`},
		{`public.pg_options_to_table(ARRAY['x=1'])`,`public.pg_options_to_table(text[])`},
		{`pg_settings_get_flags_missing('x')`,`pg_settings_get_flags_missing(unknown)`},
		{`pg_options_to_table_missing(ARRAY['x=1'])`,`pg_options_to_table_missing(text[])`},
		{`"PG_SETTINGS_GET_FLAGS"('x')`,`PG_SETTINGS_GET_FLAGS(unknown)`},
		{`"PG_OPTIONS_TO_TABLE"(ARRAY['x=1'])`,`PG_OPTIONS_TO_TABLE(text[])`},
	}
	settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
		for _,prepared := range []bool{false,true} {
			for _,tc := range cases {
				for _,prefix := range []string{"SELECT ","/*é🙂*/ SELECT ","SELECT * FROM "} {
					q := prefix+tc.call
					position := fixture.Position(len([]rune(prefix))+1)
					s := settingsLabelFailure(q,fixture.Diagnostic{Code:"42883",Message:"function "+tc.signature+" does not exist",Hint:hint,Position:position})
					s.Prepared = prepared
					if prepared { s.ErrorStage = "prepare" }
					fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
				}
			}
			for _,call := range []string{`flags_absent.pg_settings_get_flags('x')`,`flags_absent.pg_options_to_table(ARRAY['x=1'])`} {
				s := settingsLabelFailure("SELECT "+call,fixture.Diagnostic{Code:"3F000",Message:`schema "flags_absent" does not exist`,Position:"8"})
				s.Prepared = prepared
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			}
			for _,name := range []string{"PG_SETTINGS_GET_FLAGS",`"pg_settings_get_flags"`} {
				s := settingsOptionsFlags(b.PG,"SELECT "+name+"('work_mem')","{EXPLAIN}")
				s.Prepared = prepared
				fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			}
			for _,name := range []string{"PG_OPTIONS_TO_TABLE",`"pg_options_to_table"`} {
				s := settingsOptionsRows(b.PG,"SELECT * FROM "+name+"('{x=1}')",[][]any{{"x","1"}})
				s.Prepared = prepared
				fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			}
			q := `SELECT * FROM pg_options_to_table(options_array => $1)`
			s := settingsOptionsRows(b.PG,q,[][]any{{"x","1"}},`{x=1}`)
			s.Prepared = prepared
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			q = `SELECT * FROM pg_options_to_table($1::text[])`
			s = settingsOptionsNullElement(q,`{x=1,NULL}`)
			s.Prepared = prepared
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			q = `SELECT * FROM pg_options_to_table($1::text[])`
			s = settingsLabelFailure(q,fixture.Diagnostic{Code:"22P02",Message:`malformed array literal: "not-an-array"`,Detail:`Array value must start with "{" or dimension information.`,Context:"unnamed portal parameter $1 = '...'"},"not-an-array")
			s.Prepared = prepared
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
		}
		// Unknown literal and NULL bind uniquely rather than manufacturing
		// descriptive-label overloads. Whole NULL is an empty, typed source.
		for _,literal := range []string{"NULL","NULL::text[]"} {
			s := settingsOptionsRows(b.PG,"SELECT * FROM pg_options_to_table("+literal+")",nil)
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			s.Prepared = true
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
		}
	})
}
