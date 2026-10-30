package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIrishSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		irishConfigurationSettings(t, p, b, c)
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
				dict = "irish_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		irishSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'irish_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='irish'::regconfig ORDER BY maptokentype,mapseqno"
		irishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, s := range []fixture.Step{
			irishDebugStep(),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('irish','RATS-abhcóideacht 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "RATS-abhcóideacht", "{irish_stem}", "irish_stem", "{rats-abhcóid}"},
				{"hword_asciipart", "RATS", "{irish_stem}", "irish_stem", "{rats}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "abhcóideacht", "{irish_stem}", "irish_stem", "{abhcóid}"},
				{"blank", " ", "{}", nil, nil}, {"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('irish','RATS abhcóideacht agus') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abhcóid':2 'agus':3 'rats':1"}}),
			fixture.Select("SELECT to_tsvector('irish','RATS-abhcóideacht 123 abc123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':4 'abc123':5 'abhcóid':3 'rats':2 'rats-abhcóid':1"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'abhcóideacht') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('irish',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('irish','abhcóideacht') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('irish','abhcóideacht') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			irishSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION irish_copy (COPY=irish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION irish_copy ALTER MAPPING FOR asciiword WITH english_stem", 0)
		irishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('irish_copy','RATS abhcóideacht') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abhcóid':2 'rat':1"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY irish_map (TEMPLATE=snowball,Language=irish,StopWords=english)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION irish_copy ALTER MAPPING FOR asciiword,word WITH irish_map", 0)
		irishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('irish_copy','ONLY abhcóideacht') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abhcóid':2"}}))
		irishSmokeBoth(t, p, b, c, fixture.Select("SELECT alias,dictionaries,dictionary,lexemes FROM ts_debug('irish_copy','ONLY')", []string{"alias", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "{irish_map}", "irish_map", "{}"}}))
		irishSmokeBoth(t, p, b, c, irishDebugStep())
	})
}

func irishDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('irish','RATS')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{irish_stem}", "irish_stem", "{rats}"}})
}
