package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLithuanianSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		lithuanianConfigurationSettings(t, p, b, c)
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
				dict = "lithuanian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'lithuanian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='lithuanian'::regconfig ORDER BY maptokentype,mapseqno"
		lithuanianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, s := range []fixture.Step{
			lithuanianDebugStep(),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('lithuanian','VYRAS-gervė 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "VYRAS-gervė", "{lithuanian_stem}", "lithuanian_stem", "{vyras-gerv}"},
				{"hword_asciipart", "VYRAS", "{lithuanian_stem}", "lithuanian_stem", "{vyr}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "gervė", "{lithuanian_stem}", "lithuanian_stem", "{gerv}"},
				{"blank", " ", "{}", nil, nil}, {"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('lithuanian','vyras ir gervė THE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gerv':3 'ir':2 'the':4 'vyr':1"}}),
			fixture.Select("SELECT to_tsvector('lithuanian','sesers seseris seserimis') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ses':1 'seser':2,3"}}),
			fixture.Select("SELECT to_tsvector('lithuanian','VYRAS-gervė 123 abc123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':4 'abc123':5 'gerv':3 'vyr':2 'vyras-gerv':1"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'gervė') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('lithuanian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('lithuanian','gervė') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('lithuanian','gervė') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			lithuanianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION lithuanian_copy (COPY=lithuanian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION lithuanian_copy ALTER MAPPING FOR asciiword WITH english_stem", 0)
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('lithuanian_copy','RUNNING gervė') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gerv':2 'run':1"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY lithuanian_map (TEMPLATE=snowball,Language=lithuanian,StopWords=english)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION lithuanian_copy ALTER MAPPING FOR asciiword,word WITH lithuanian_map", 0)
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('lithuanian_copy','ONLY gervė') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gerv':2"}}))
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT alias,dictionaries,dictionary,lexemes FROM ts_debug('lithuanian_copy','ONLY')", []string{"alias", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "{lithuanian_map}", "lithuanian_map", "{}"}}))
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "lithuanian_map", "lithuanian_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION lithuanian_copy ALTER MAPPING FOR asciiword,lithuanian_bad_token WITH simple", "22023", `token type "lithuanian_bad_token" does not exist`)
		preserved()
		lithuanianSmokeBoth(t, p, b, c, lithuanianDebugStep())
	})
}

func lithuanianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('lithuanian','vyras gervė')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "vyras", "{lithuanian_stem}", "lithuanian_stem", "{vyr}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"word", "Word, all letters", "gervė", "{lithuanian_stem}", "lithuanian_stem", "{gerv}"},
	})
}
