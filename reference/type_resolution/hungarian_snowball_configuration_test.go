package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHungarianSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
				dict = "hungarian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'hungarian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='hungarian'::regconfig ORDER BY maptokentype,mapseqno"
		hungarianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		hungarianSmokeBoth(t, p, b, c, hungarianDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('hungarian','RATS')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{hungarian_stem}", "hungarian_stem", "{rats}"}}),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('hungarian','RATS-házak 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "RATS-házak", "{hungarian_stem}", "hungarian_stem", "{rats-ház}"},
				{"hword_asciipart", "RATS", "{hungarian_stem}", "hungarian_stem", "{rats}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "házak", "{hungarian_stem}", "hungarian_stem", "{ház}"},
				{"blank", " ", "{}", nil, nil},
				{"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil},
				{"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('hungarian','a házak és házban') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ház':2,4"}}),
			fixture.Select("SELECT to_tsvector('hungarian','RATS-házak 123 abc123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':4 'abc123':5 'ház':3 'rats':2 'rats-ház':1"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'házak') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('hungarian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('hungarian','házak') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('hungarian','házak') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			hungarianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION hungarian_copy (COPY=hungarian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION hungarian_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('hungarian_copy','a házak') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'a':1 'ház':2"}}))
		hungarianSmokeBoth(t, p, b, c, hungarianDebugStep())
	})
}
