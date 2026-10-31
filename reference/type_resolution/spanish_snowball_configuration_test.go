package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func spanishDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('spanish','CASAS')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "CASAS", "{spanish_stem}", "spanish_stem", "{cas}"}})
}

func TestReferenceSpanishSnowballConfiguration(t *testing.T) {
	t.Run("configuration-conversion", spanishConfigurationSettings)
	t.Run("token-routing-and-chain-termination", spanishTokenRouting)
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{
			"SELECT * FROM ts_debug($1::regconfig,$2::text)",
			"SELECT * FROM ts_debug($1,$2)",
		} {
			ascii := spanishDebugStep()
			ascii.Query = query
			ascii.Args = []any{"spanish", "CASAS"}
			spanishSmokeBoth(t, p, b, c, ascii)
			accented := fixture.Select(query,
				[]string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"},
				[]string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"},
				[][]any{{"word", "Word, all letters", "niños", "{spanish_stem}", "spanish_stem", "{niñ}"}},
				[]byte("spanish"), []byte("niños"))
			spanishSmokeBoth(t, p, b, c, accented)
		}
		for _, query := range []string{
			"SELECT to_tsvector($1::regconfig,$2::text) AS normalized",
			"SELECT to_tsvector($1,$2) AS normalized",
		} {
			spanishSmokeBoth(t, p, b, c, fixture.Select(query,
				[]string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'niñ':4"}},
				"spanish", "las casas y niños"))
			for _, args := range [][]any{{nil, "casas"}, {"spanish", nil}} {
				spanishSmokeBoth(t, p, b, c, fixture.Select(query,
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
				dict = "spanish_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'spanish_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='spanish'::regconfig ORDER BY maptokentype,mapseqno"
		spanishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		spanishSmokeBoth(t, p, b, c, spanishDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('spanish','niños')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "niños", "{spanish_stem}", "spanish_stem", "{niñ}"}}),
			fixture.Select("SELECT to_tsvector('spanish','las casas y niños') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'niñ':4"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'casas') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('spanish',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('spanish','casas') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('spanish','casas') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			spanishSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION spanish_copy (COPY=spanish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION spanish_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('spanish_copy','las casas y niños') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'casas':2 'las':1 'niñ':4 'y':3"}}))
			spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('spanish','las casas y niños') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':2 'niñ':4"}}))
		}
		check()
		preserved := spanishDefinitionSnapshot(t, p, b, c, "spanish_stem", "spanish_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION spanish_copy ALTER MAPPING FOR asciiword,spanish_missing_alias WITH spanish_stem", "22023", `token type "spanish_missing_alias" does not exist`)
		preserved()
		check()
	})
}
