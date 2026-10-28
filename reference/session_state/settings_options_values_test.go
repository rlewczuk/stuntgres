package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsOptionsFlags(t *testing.T) {
	cases := []struct{name string; want any}{
		{"enable_seqscan","{EXPLAIN}"},{"work_mem","{EXPLAIN}"},{"WORK_MEM","{EXPLAIN}"},
		{"sort_mem","{EXPLAIN}"},{"vacuum_mem","{}"},{"ssl_ecdh_curve","{}"},
		{"application_name","{NOT_IN_SAMPLE}"},{"config_file","{}"},{"max_stack_depth","{}"},
		{"is_superuser","{NO_RESET_ALL,NO_SHOW_ALL,NOT_IN_SAMPLE}"},
		{"role","{NO_RESET_ALL,NO_SHOW_ALL,NOT_IN_SAMPLE}"},
		{"session_authorization","{NO_RESET_ALL,NO_SHOW_ALL,NOT_IN_SAMPLE}"},
		{"seed","{NO_RESET,NO_RESET_ALL,NO_SHOW_ALL,NOT_IN_SAMPLE}"},
		{"transaction_isolation","{NO_RESET,NO_RESET_ALL,NOT_IN_SAMPLE}"},
		{"max_index_keys","{NOT_IN_SAMPLE}"},{"server_version","{NOT_IN_SAMPLE}"},
		{"data_checksums","{NOT_IN_SAMPLE,RUNTIME_COMPUTED}"},
		{"data_directory_mode","{NOT_IN_SAMPLE,RUNTIME_COMPUTED}"},
		{"shared_memory_size","{NOT_IN_SAMPLE,RUNTIME_COMPUTED}"},
		{"wal_segment_size","{NOT_IN_SAMPLE,RUNTIME_COMPUTED}"},
		{"unknown_flags",nil},{"unknown_flags.setting",nil},{"",nil},{"bad..name",nil},
	}
	settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
		for _, prepared := range []bool{false,true} {
			for _,tc := range cases {
				s := settingsOptionsFlags(b.PG,`SELECT pg_catalog.pg_settings_get_flags($1)`,tc.want,tc.name)
				s.Prepared = prepared
				fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			}
			s := settingsOptionsFlags(b.PG,`SELECT pg_settings_get_flags(NULL)`,nil)
			s.Prepared = prepared
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
		}
	})
}

func TestReferenceSettingsOptionsValues(t *testing.T) {
	cases := []struct{input string; rows [][]any}{
		{`ARRAY['x=1','y=two']`,[][]any{{"x","1"},{"y","two"}}},
		{`ARRAY['a=b=c','bare','empty=','=value','',' x = y ']`,[][]any{{"a","b=c"},{"bare",nil},{"empty",""},{"","value"},{"",nil},{" x "," y "}}},
		{`ARRAY['x=1','x=1']`,[][]any{{"x","1"},{"x","1"}}},
		{`NULL::text[]`,nil},{`'{}'::text[]`,nil},
		{`'[0:1][3:4]={{a=1,b=2},{c=3,d=4}}'::text[]`,[][]any{{"a","1"},{"b","2"},{"c","3"},{"d","4"}}},
		{"ARRAY['é🙂=line\n\\quote\"']",[][]any{{"é🙂","line\n\\quote\""}}},
	}
	settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
		for _, prepared := range []bool{false,true} {
			for _,tc := range cases {
				for _,prefix := range []string{"SELECT * FROM pg_options_to_table(","SELECT * FROM pg_catalog.pg_options_to_table(options_array => "} {
					s := settingsOptionsRows(b.PG,prefix+tc.input+")",tc.rows)
					s.Prepared = prepared
					fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
				}
			}
			for _,input := range []string{`ARRAY[NULL::text]`,`ARRAY['x=1',NULL::text]`} {
				s := settingsOptionsNullElement(`SELECT * FROM pg_options_to_table(`+input+`)`)
				s.Prepared = prepared
				fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			}
			alias := fixture.Select(`SELECT o.n,o.v FROM pg_options_to_table(ARRAY['x=1','y=two']) AS o(n,v)`,[]string{"n","v"},[]string{"TEXT","TEXT"},[][]any{{"x","1"},{"y","two"}})
			alias.Prepared,alias.Bag = prepared,true
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,alias)
			ordinal := fixture.Select(`SELECT ordinality FROM pg_options_to_table(ARRAY['x=1','x=1']) WITH ORDINALITY ORDER BY ordinality`,[]string{"ordinality"},[]string{"INT8"},[][]any{{int64(1)},{int64(2)}})
			ordinal.Prepared = prepared
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,ordinal)
		}
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Exec(`CREATE TABLE options_inputs(id integer,a text[])`,0))
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Exec(`INSERT INTO options_inputs VALUES (1,ARRAY['x=1','x=1']),(2,ARRAY['y=two']),(3,NULL),(4,'{}')`,4))
		for _,prepared := range []bool{false,true} {
			s := fixture.Select(`SELECT i.id,o.option_name,o.option_value FROM options_inputs AS i,pg_options_to_table(i.a) AS o`,[]string{"id","option_name","option_value"},[]string{"INT4","TEXT","TEXT"},[][]any{{int64(1),"x","1"},{int64(1),"x","1"},{int64(2),"y","two"}})
			s.Prepared,s.Bag = prepared,true
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			s = fixture.Select(`SELECT i.id,o.ordinality FROM options_inputs AS i,pg_options_to_table(i.a) WITH ORDINALITY AS o ORDER BY i.id,o.ordinality`,[]string{"id","ordinality"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(1)},{int64(1),int64(2)},{int64(2),int64(1)}})
			s.Prepared = prepared
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
		}
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Exec(`CREATE TABLE options_atomic(n text,v text)`,0))
		fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Exec(`INSERT INTO options_atomic VALUES ('sentinel','kept')`,1))
		for _,prepared := range []bool{false,true} {
			s := fixture.ImplicitError(`INSERT INTO options_atomic SELECT * FROM pg_options_to_table(ARRAY['x=1',NULL::text])`,fixture.Diagnostic{Code:"22004",Message:"null array element not allowed in this context"})
			s.Prepared,s.ErrorStage = prepared,"execute"
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
			fixture.RunStep(t,p.Ctx,owner,b.Recorder,fixture.Select(`SELECT n,v FROM options_atomic`,[]string{"n","v"},[]string{"TEXT","TEXT"},[][]any{{"sentinel","kept"}}))
		}
	})
}

func TestReferenceSettingsOptionsMetadata(t *testing.T) {
	for _,prepared := range []bool{false,true} {
		t.Run(fmt.Sprint(prepared),func(t *testing.T){
			settingsLabelScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,owner,peer *sql.Conn){
				for _,name := range []any{"enable_seqscan","config_file","missing_options",nil} {
					var want any
					if name == "enable_seqscan" { want = "{EXPLAIN}" }; if name == "config_file" { want = "{}" }
					s := settingsOptionsFlags(b.PG,`SELECT pg_settings_get_flags($1)`,want,name)
					s.Prepared = prepared
					fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
				}
				for _,tc := range []struct{input string; rows [][]any}{{`ARRAY['x=1']`,[][]any{{"x","1"}}},{`NULL::text[]`,nil},{`'{}'::text[]`,nil}} {
					s := settingsOptionsRows(b.PG,`SELECT * FROM pg_options_to_table(`+tc.input+`)`,tc.rows)
					s.Prepared = prepared
					fixture.RunStep(t,p.Ctx,owner,b.Recorder,s)
				}
			})
		})
	}
}
