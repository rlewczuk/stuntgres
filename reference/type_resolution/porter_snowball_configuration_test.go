package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func porterDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('porter_config','SKIES')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "SKIES", "{porter_dict}", "porter_dict", "{ski}"}})
}

func TestReferencePorterSnowballConfiguration(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		porterSetup(t, p, b, c, "porter_dict", "porter_config", true)
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
				dict = "porter_dict"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'porter_dict'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='porter_config'::regconfig ORDER BY maptokentype,mapseqno"
		porterSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		porterSmokeBoth(t, p, b, c, porterDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('porter_config','cafés')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "cafés", "{porter_dict}", "porter_dict", "{café}"}}),
			fixture.Select("SELECT to_tsvector('porter_config','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':3 'ski':2"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'biler') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('porter_config',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('porter_config','biler') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('porter_config','biler') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			porterSmokeBoth(t, p, b, c, s)
		}
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('porter_config','ONLY')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "ONLY", "{porter_dict}", "porter_dict", "{}"}}))
		porterSetup(t, p, b, c, "porter_single", "", false)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION porter_single_config (COPY=simple)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION porter_single_config ALTER MAPPING FOR asciiword WITH porter_single", 0)
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('porter_single_config','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':3 'ski':2 'the':1"}}))
		porterSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('porter_single_config','cafés') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cafés':1"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION porter_copy (COPY=porter_config)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION porter_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('porter_copy','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dying':3 'skies':2 'the':1"}}))
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('porter_config','The skies dying') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':3 'ski':2"}}))
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "porter_dict", "porter_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION porter_copy ALTER MAPPING FOR asciiword,porter_missing_alias WITH porter_stem", "22023", `token type "porter_missing_alias" does not exist`)
		preserved()
		check()
	})
}
