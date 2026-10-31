package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTamilSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		tamilConfigurationSettings(t, p, b, c)
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
				dict = "tamil_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'tamil_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='tamil'::regconfig ORDER BY maptokentype,mapseqno"
		tamilSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, s := range []fixture.Step{
			tamilDebugStep(),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('tamil','RUNNING-தமிழில் 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "RUNNING-தமிழில்", "{tamil_stem}", "tamil_stem", "{running-தமிழ்}"},
				{"hword_asciipart", "RUNNING", "{tamil_stem}", "tamil_stem", "{running}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "தமிழில்", "{tamil_stem}", "tamil_stem", "{தமிழ்}"},
				{"blank", " ", "{}", nil, nil}, {"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('tamil','THE மரத்தில் மரம் RUNNING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'running':4 'the':1 'மரம்':2,3"}}),
			fixture.Select("SELECT to_tsvector('tamil','மரத்தில் மரம் புத்தகங்கள்') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'புத்தகம்':3 'மரம்':1,2"}}),
			fixture.Select("SELECT to_tsvector('tamil','RUNNING-தமிழில் 123 abc123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':4 'abc123':5 'running':2 'running-தமிழ்':1 'தமிழ்':3"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'தமிழில்') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('tamil',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('tamil','தமிழில்') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('tamil','தமிழில்') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			tamilSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION tamil_copy (COPY=tamil)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION tamil_copy ALTER MAPPING FOR asciiword WITH english_stem", 0)
		tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('tamil_copy','RUNNING தமிழில்') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'run':1 'தமிழ்':2"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY tamil_map (TEMPLATE=snowball,Language=tamil,StopWords=english)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION tamil_copy ALTER MAPPING FOR asciiword,word WITH tamil_map", 0)
		tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('tamil_copy','ONLY தமிழில்') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'தமிழ்':2"}}))
		tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT alias,dictionaries,dictionary,lexemes FROM ts_debug('tamil_copy','ONLY')", []string{"alias", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "{tamil_map}", "tamil_map", "{}"}}))
		preserved := tamilDefinitionSnapshot(t, p, b, c, "tamil_map", "tamil_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION tamil_copy ALTER MAPPING FOR asciiword,tamil_bad_token WITH simple", "22023", `token type "tamil_bad_token" does not exist`)
		preserved()
		tamilSmokeBoth(t, p, b, c, tamilDebugStep())
	})
}

func tamilDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('tamil','RUNNING மரத்தில்')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "RUNNING", "{tamil_stem}", "tamil_stem", "{running}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"word", "Word, all letters", "மரத்தில்", "{tamil_stem}", "tamil_stem", "{மரம்}"},
	})
}
