package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func nepaliDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('nepali','RATS घरहरू')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "RATS", "{nepali_stem}", "nepali_stem", "{rats}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"word", "Word, all letters", "घरहरू", "{nepali_stem}", "nepali_stem", "{घर}"},
	})
}

func TestReferenceNepaliSnowballConfiguration(t *testing.T) {
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
				dict = "nepali_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'nepali_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='nepali'::regconfig ORDER BY maptokentype,mapseqno"
		nepaliSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		nepaliSmokeBoth(t, p, b, c, nepaliDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('nepali','RATS-घरहरू 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "RATS-घरहरू", "{nepali_stem}", "nepali_stem", "{rats-घर}"},
				{"hword_asciipart", "RATS", "{nepali_stem}", "nepali_stem", "{rats}"}, {"blank", "-", "{}", nil, nil},
				{"hword_part", "घरहरू", "{nepali_stem}", "nepali_stem", "{घर}"}, {"blank", " ", "{}", nil, nil},
				{"uint", "123", "{simple}", "simple", "{123}"}, {"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('nepali','RATS र घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':1 'घर':3"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('nepali',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('nepali','घरहरू') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
		} {
			nepaliSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION nepali_copy (COPY=nepali)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION nepali_copy ALTER MAPPING FOR asciiword WITH english_stem,simple", 0)
		nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('nepali_copy','RATS घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rat':1 'घर':2"}}))
		preserved := nepaliDefinitionSnapshot(t, p, b, c, "nepali_stem", "nepali_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION nepali_copy ALTER MAPPING FOR asciiword,nepali_missing_alias WITH simple", "22023", `token type "nepali_missing_alias" does not exist`)
		preserved()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION nepali_copy DROP MAPPING FOR word", 0)
		nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('nepali_copy','RATS घरहरू') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rat':1"}}))
		regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict::regdictionary::text AS dictionary FROM pg_ts_config_map WHERE mapcfg='nepali_copy'::regconfig AND maptokentype=1 ORDER BY mapseqno", []string{"mapseqno", "dictionary"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "english_stem"}, {int64(2), "simple"}})
	})
}
