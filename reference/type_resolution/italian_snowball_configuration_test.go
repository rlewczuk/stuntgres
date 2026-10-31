package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func italianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('italian','CANI')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "CANI", "{italian_stem}", "italian_stem", "{can}"}})
}

func TestReferenceItalianSnowballConfiguration(t *testing.T) {
	t.Run("configuration-conversion", italianConfigurationSettings)
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
				dict = "italian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		italianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'italian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='italian'::regconfig ORDER BY maptokentype,mapseqno"
		italianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		italianSmokeBoth(t, p, b, c, italianDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('italian','CANI-caffè 123 abc123')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"hword", "CANI-caffè", "{italian_stem}", "italian_stem", "{cani-caff}"},
				{"hword_asciipart", "CANI", "{italian_stem}", "italian_stem", "{can}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "caffè", "{italian_stem}", "italian_stem", "{caff}"},
				{"blank", " ", "{}", nil, nil}, {"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil}, {"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('italian','gatti-cani')", []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{"asciihword", "gatti-cani", "{italian_stem}", "italian_stem", "{gatti-can}"},
				{"hword_asciipart", "gatti", "{italian_stem}", "italian_stem", "{gatt}"}, {"blank", "-", "{}", nil, nil},
				{"hword_asciipart", "cani", "{italian_stem}", "italian_stem", "{can}"},
			}),
			fixture.Select("SELECT * FROM ts_debug('italian','caffè')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "caffè", "{italian_stem}", "italian_stem", "{caff}"}}),
			fixture.Select("SELECT to_tsvector('italian','i gatti e i cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'can':5 'gatt':2"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('italian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('italian','cani') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('italian','cani') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			italianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION italian_copy (COPY=italian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION italian_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		italianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('italian_copy','i cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cani':2 'i':1"}}))
		preserved := italianDefinitionSnapshot(t, p, b, c, "italian_stem", "italian_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION italian_copy ALTER MAPPING FOR asciiword,italian_missing_alias WITH italian_stem", "22023", `token type "italian_missing_alias" does not exist`)
		preserved()
		italianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('italian_copy','i cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cani':2 'i':1"}}))
		italianSmokeBoth(t, p, b, c, italianDebugStep())
	})
}
