package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func norwegianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('norwegian','BILER')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "BILER", "{norwegian_stem}", "norwegian_stem", "{bil}"}})
}

func TestReferenceNorwegianSnowballConfiguration(t *testing.T) {
	t.Run("configuration-conversion", norwegianConfigurationSettings)
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
				dict = "norwegian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'norwegian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='norwegian'::regconfig ORDER BY maptokentype,mapseqno"
		norwegianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		norwegianSmokeBoth(t, p, b, c, norwegianDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('norwegian','båtene')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "båtene", "{norwegian_stem}", "norwegian_stem", "{båt}"}}),
			fixture.Select("SELECT to_tsvector('norwegian','og biler på båtene') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'bil':2 'båt':4"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'biler') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('norwegian',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('norwegian','biler') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('norwegian','biler') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			norwegianSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION norwegian_copy (COPY=norwegian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION norwegian_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('norwegian_copy','og biler på båtene') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'biler':2 'båt':4 'og':1"}}))
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('norwegian','og biler på båtene') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'bil':2 'båt':4"}}))
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "norwegian_stem", "norwegian_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION norwegian_copy ALTER MAPPING FOR asciiword,norwegian_missing_alias WITH norwegian_stem", "22023", `token type "norwegian_missing_alias" does not exist`)
		preserved()
		check()
	})
}
