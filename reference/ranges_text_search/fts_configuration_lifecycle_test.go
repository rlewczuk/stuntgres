package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSConfigurationDDL(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "fts_config_destination")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_original (COPY=pg_catalog.simple)", 0))
		run(ftsConfigurationMaps("public.config_original", ftsConfigurationSimpleMaps()))
		run(fixture.Select("SELECT cfgparser::bigint, cfgowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owned FROM pg_ts_config WHERE oid='public.config_original'::regconfig",
			[]string{"cfgparser", "owned"}, []string{"INT8", "BOOL"}, [][]any{{int64(3722), true}}))
		run(fixture.Prepared(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_copy (COPY=public.config_original)", 0)))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_empty (PARSER=pg_catalog.default)", 0))
		run(ftsConfigurationMaps("public.config_empty", nil))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				options string
				maps    [][]any
			}{
				{"COPY=pg_catalog.simple,COPY=public.config_empty", nil},
				{"COPY=public.config_empty,COPY=pg_catalog.simple", ftsConfigurationSimpleMaps()},
				{"PARSER=pg_catalog.default,PARSER=pg_catalog.default", nil},
				{"PARSER='default'", nil},
				{"COPY='simple'", ftsConfigurationSimpleMaps()},
			} {
				s := fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_repeat ("+tc.options+")", 0)
				s.Prepared = prepared
				run(s)
				run(ftsConfigurationMaps("public.config_repeat", tc.maps))
				run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_repeat", 0))
			}
			for _, tc := range []struct{ options, code, message string }{
				{"COPY=public.config_repeat_absent,COPY=pg_catalog.simple", "42704", `text search configuration "public.config_repeat_absent" does not exist`},
				{"PARSER=config_repeat_absent,PARSER=pg_catalog.default", "42704", `text search parser "config_repeat_absent" does not exist`},
			} {
				s := ftsConfigurationError("CREATE TEXT SEARCH CONFIGURATION public.config_repeat ("+tc.options+")", tc.code, tc.message)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname='config_repeat'", []string{"cfgname"}, []string{"NAME"}, nil))
			}
		}
		run(ftsConfigurationVector("SELECT to_tsvector('public.config_empty'::regconfig,'The Fat Rats') AS normalized", ""))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_original DROP MAPPING FOR asciiword", 0))
		run(ftsConfigurationMaps("public.config_copy", ftsConfigurationSimpleMaps()))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_original", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('public.config_copy'::regconfig,'The Fat Rats') AS normalized", "'fat':2 'rats':3 'the':1"))
		run(fixture.Exec("CREATE SCHEMA fts_config_destination", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_copy RENAME TO config_renamed", 0))
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_renamed SET SCHEMA fts_config_destination", 0)))
		run(ftsConfigurationMaps("fts_config_destination.config_renamed", ftsConfigurationSimpleMaps()))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION fts_config_destination.config_renamed, public.config_empty RESTRICT", 0))
		run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname IN ('config_original','config_copy','config_renamed','config_empty')", []string{"cfgname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceFTSConfigurationMappings(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_maps (PARSER=pg_catalog.default)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_maps ADD MAPPING FOR asciiword,asciiword WITH pg_catalog.simple,pg_catalog.simple", 0))
		run(ftsConfigurationMaps("public.config_maps", [][]any{{int64(1), int64(1), int64(3765)}, {int64(1), int64(2), int64(3765)}}))
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_maps ALTER MAPPING FOR asciiword,numword WITH pg_catalog.simple", 0)))
		want := [][]any{{int64(1), int64(1), int64(3765)}, {int64(3), int64(1), int64(3765)}}
		run(ftsConfigurationMaps("public.config_maps", want))
		run(ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION public.config_maps ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`))
		run(ftsConfigurationMaps("public.config_maps", want))
		run(ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION public.config_maps ALTER MAPPING FOR asciiword WITH config_missing_dictionary", "42704", `text search dictionary "config_missing_dictionary" does not exist`))
		run(ftsConfigurationMaps("public.config_maps", want))
		run(ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION public.config_maps DROP MAPPING FOR asciiword,word", "42704", `mapping for token type "word" does not exist`))
		run(ftsConfigurationMaps("public.config_maps", want))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.config_replacement (TEMPLATE=pg_catalog.simple)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_maps ALTER MAPPING FOR asciiword REPLACE pg_catalog.simple WITH public.config_replacement", 0))
		run(fixture.Select("SELECT mapdict='public.config_replacement'::regdictionary::oid AS replaced FROM pg_ts_config_map WHERE mapcfg='public.config_maps'::regconfig AND maptokentype=1",
			[]string{"replaced"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_maps ALTER MAPPING REPLACE public.config_replacement WITH pg_catalog.simple", 0))
		run(ftsConfigurationMaps("public.config_maps", want))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_maps DROP MAPPING FOR asciiword,numword", 0))
		run(ftsConfigurationMaps("public.config_maps", nil))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_maps", 0))
		run(fixture.Exec("DROP TEXT SEARCH DICTIONARY public.config_replacement RESTRICT", 0))
	})
}
