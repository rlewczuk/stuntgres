package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func portugueseDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('portuguese','CASAS')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "CASAS", "{portuguese_stem}", "portuguese_stem", "{cas}"}})
}

func TestReferencePortugueseSnowballConfiguration(t *testing.T) {
	t.Run("configuration-conversion", portugueseConfigurationSettings)
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{
			"SELECT * FROM ts_debug($1::regconfig,$2::text)",
			"SELECT * FROM ts_debug($1,$2)",
		} {
			ascii := portugueseDebugStep()
			ascii.Query = query
			ascii.Args = []any{"portuguese", "CASAS"}
			portugueseSmokeBoth(t, p, b, c, ascii)
			accented := fixture.Select(query,
				[]string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"},
				[]string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"},
				[][]any{{"word", "Word, all letters", "corações", "{portuguese_stem}", "portuguese_stem", "{coraçõ}"}},
				[]byte("portuguese"), []byte("corações"))
			portugueseSmokeBoth(t, p, b, c, accented)
		}
		for _, query := range []string{
			"SELECT to_tsvector($1::regconfig,$2::text) AS normalized",
			"SELECT to_tsvector($1,$2) AS normalized",
		} {
			portugueseSmokeBoth(t, p, b, c, fixture.Select(query,
				[]string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'coraçõ':4"}},
				"portuguese", "as casas e corações"))
			for _, args := range [][]any{{nil, "casas"}, {"portuguese", nil}} {
				portugueseSmokeBoth(t, p, b, c, fixture.Select(query,
					[]string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}, args...))
			}
		}
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
				dict = "portuguese_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'portuguese_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='portuguese'::regconfig ORDER BY maptokentype,mapseqno"
		portugueseSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		portugueseSmokeBoth(t, p, b, c, portugueseDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('portuguese','corações')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "corações", "{portuguese_stem}", "portuguese_stem", "{coraçõ}"}}),
			fixture.Select("SELECT to_tsvector('portuguese','as casas e corações') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'coraçõ':4"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'casas') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('portuguese',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('portuguese','casas') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('portuguese','casas') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			portugueseSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION portuguese_copy (COPY=portuguese)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION portuguese_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('portuguese_copy','as casas e corações') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'as':1 'casas':2 'coraçõ':4 'e':3"}}))
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('portuguese','as casas e corações') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'coraçõ':4"}}))
		}
		check()
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "portuguese_stem", "portuguese_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION portuguese_copy ALTER MAPPING FOR asciiword,portuguese_missing_alias WITH portuguese_stem", "22023", `token type "portuguese_missing_alias" does not exist`)
		preserved()
		check()
	})
}
