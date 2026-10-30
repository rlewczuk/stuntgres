package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndonesianSnowballConfiguration(t *testing.T) {
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
				dict = "indonesian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'indonesian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='indonesian'::regconfig ORDER BY maptokentype,mapseqno"
		indonesianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		indonesianSmokeBoth(t, p, b, c, indonesianDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('indonesian','RATS')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{indonesian_stem}", "indonesian_stem", "{rats}"}}),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('indonesian','MENYAPU 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"asciiword", "MENYAPU", "{indonesian_stem}", "indonesian_stem", "{sapu}"},
				{"blank", " ", "{}", nil, nil},
				{"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil},
				{"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT to_tsvector('indonesian','Pelajaran menyapu buku dan televisi') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ajar':1 'buku':3 'dan':4 'sapu':2 'televisi':5"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'MENYAPU') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('indonesian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('indonesian','MENYAPU') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('indonesian','MENYAPU') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			indonesianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION indonesian_copy (COPY=indonesian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION indonesian_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('indonesian_copy','MENYAPU dan') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dan':2 'menyapu':1"}}))
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION indonesian_copy ALTER MAPPING FOR asciiword,indonesian_bad_token WITH indonesian_stem", "22023", `token type "indonesian_bad_token" does not exist`)
		indonesianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('indonesian_copy','MENYAPU dan') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dan':2 'menyapu':1"}}))
		indonesianSmokeBoth(t, p, b, c, indonesianDebugStep())
	})
}
