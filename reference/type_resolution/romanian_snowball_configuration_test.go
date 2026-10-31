package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRomanianSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		romanianConfigurationSettings(t, p, b, c)
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
				dict = "romanian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'romanian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig ORDER BY maptokentype,mapseqno"
		romanianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, s := range []fixture.Step{
			romanianDebugStep(),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('romanian','OCOLUL-abruptă 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "OCOLUL-abruptă", "{romanian_stem}", "romanian_stem", "{ocolul-abrupt}"},
				{"hword_asciipart", "OCOLUL", "{romanian_stem}", "romanian_stem", "{ocol}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "abruptă", "{romanian_stem}", "romanian_stem", "{abrupt}"},
				{"blank", " ", "{}", nil, nil}, {"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('romanian','ocolul și abruptă THE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abrupt':3 'ocol':1 'the':4 'și':2"}}),
			fixture.Select("SELECT to_tsvector('romanian','OCOLUL și ocolește') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ocol':1,3 'și':2"}}),
			fixture.Select("SELECT to_tsvector('romanian','OCOLUL-abruptă 123 abc123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':4 'abc123':5 'abrupt':3 'ocol':2 'ocolul-abrupt':1"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'abruptă') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('romanian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('romanian','abruptă') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('romanian','abruptă') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			romanianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION romanian_copy (COPY=romanian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION romanian_copy ALTER MAPPING FOR asciiword WITH english_stem", 0)
		romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('romanian_copy','RUNNING abruptă') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abrupt':2 'run':1"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY romanian_map (TEMPLATE=snowball,Language=romanian,StopWords=english)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION romanian_copy ALTER MAPPING FOR asciiword,word WITH romanian_map", 0)
		romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('romanian_copy','ONLY abruptă') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abrupt':2"}}))
		romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT alias,dictionaries,dictionary,lexemes FROM ts_debug('romanian_copy','ONLY')", []string{"alias", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "{romanian_map}", "romanian_map", "{}"}}))
		preserved := romanianDefinitionSnapshot(t, p, b, c, "romanian_map", "romanian_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION romanian_copy ALTER MAPPING FOR asciiword,romanian_bad_token WITH simple", "22023", `token type "romanian_bad_token" does not exist`)
		preserved()
		romanianSmokeBoth(t, p, b, c, romanianDebugStep())
	})
}

func romanianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('romanian','ocolul abruptă')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "ocolul", "{romanian_stem}", "romanian_stem", "{ocol}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"word", "Word, all letters", "abruptă", "{romanian_stem}", "romanian_stem", "{abrupt}"},
	})
}
