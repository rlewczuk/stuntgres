package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSerbianSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		serbianConfigurationSettings(t, p, b, c)
		aliases := []string{"asciiword", "word", "numword", "email", "url", "host", "sfloat", "version", "hword_numpart", "hword_part", "hword_asciipart", "blank", "tag", "protocol", "numhword", "asciihword", "hword", "url_path", "file", "float", "int", "uint", "entity"}
		var tokens, maps [][]any
		for i, alias := range aliases {
			id := int64(i + 1)
			tokens = append(tokens, []any{id, alias})
			switch id {
			case 12, 13, 14, 23:
				continue
			}
			dict := "simple"
			switch id {
			case 1, 2, 10, 11, 16, 17:
				dict = "serbian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'serbian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='serbian'::regconfig ORDER BY maptokentype,mapseqno"
		serbianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, s := range []fixture.Step{
			serbianDebugStep(),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('serbian','ABAKUSA-рад 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "ABAKUSA-рад", "{serbian_stem}", "serbian_stem", "{abakusa-rad}"},
				{"hword_asciipart", "ABAKUSA", "{serbian_stem}", "serbian_stem", "{abakus}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "рад", "{serbian_stem}", "serbian_stem", "{rad}"},
				{"blank", " ", "{}", nil, nil}, {"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('serbian','abakusa ir рад THE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abakus':1 'ir':2 'rad':3 'the':4"}}),
			fixture.Select("SELECT to_tsvector('serbian','acetat acetata') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'aceta':1 'acetat':2"}}),
			fixture.Select("SELECT to_tsvector('serbian','ABAKUSA-рад 123 abc123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':4 'abakus':2 'abakusa-rad':1 'abc123':5 'rad':3"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'рад') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('serbian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('serbian','рад') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('serbian','рад') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			serbianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION serbian_copy (COPY=serbian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION serbian_copy ALTER MAPPING FOR asciiword WITH english_stem", 0)
		serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('serbian_copy','RUNNING рад') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rad':2 'run':1"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY serbian_map (TEMPLATE=snowball,Language=serbian,StopWords=english)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION serbian_copy ALTER MAPPING FOR asciiword,word WITH serbian_map", 0)
		serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('serbian_copy','ONLY рад') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rad':2"}}))
		serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT alias,dictionaries,dictionary,lexemes FROM ts_debug('serbian_copy','ONLY')", []string{"alias", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "{serbian_map}", "serbian_map", "{}"}}))
		preserved := serbianDefinitionSnapshot(t, p, b, c, "serbian_map", "serbian_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION serbian_copy ALTER MAPPING FOR asciiword,serbian_bad_token WITH simple", "22023", `token type "serbian_bad_token" does not exist`)
		preserved()
		serbianSmokeBoth(t, p, b, c, serbianDebugStep())
	})
}

func serbianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('serbian','ABAKUSA рад')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "ABAKUSA", "{serbian_stem}", "serbian_stem", "{abakus}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"word", "Word, all letters", "рад", "{serbian_stem}", "serbian_stem", "{rad}"},
	})
}
