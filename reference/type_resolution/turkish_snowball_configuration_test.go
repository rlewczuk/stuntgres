package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func turkishDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('turkish','kitaplar')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "kitaplar", "{turkish_stem}", "turkish_stem", "{kitap}"}})
}

func TestReferenceTurkishSnowballConfiguration(t *testing.T) {
	t.Run("configuration-conversion", turkishConfigurationSettings)
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{
			"SELECT * FROM ts_debug($1::regconfig,$2::text)",
			"SELECT * FROM ts_debug($1,$2)",
		} {
			ascii := turkishDebugStep()
			ascii.Query = query
			ascii.Args = []any{"turkish", "kitaplar"}
			turkishSmokeBoth(t, p, b, c, ascii)
			accented := fixture.Select(query,
				[]string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"},
				[]string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"},
				[][]any{{"word", "Word, all letters", "ağacı", "{turkish_stem}", "turkish_stem", "{ağaç}"}},
				[]byte("turkish"), []byte("ağacı"))
			turkishSmokeBoth(t, p, b, c, accented)
		}
		for _, query := range []string{
			"SELECT to_tsvector($1::regconfig,$2::text) AS normalized",
			"SELECT to_tsvector($1,$2) AS normalized",
		} {
			turkishSmokeBoth(t, p, b, c, fixture.Select(query,
				[]string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'kitap':2"}},
				"turkish", "biz kitaplar"))
			for _, args := range [][]any{{nil, "kitaplar"}, {"turkish", nil}} {
				turkishSmokeBoth(t, p, b, c, fixture.Select(query,
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
				dict = "turkish_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'turkish_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='turkish'::regconfig ORDER BY maptokentype,mapseqno"
		turkishSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		turkishSmokeBoth(t, p, b, c, turkishDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('turkish','ağacı')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "ağacı", "{turkish_stem}", "turkish_stem", "{ağaç}"}}),
			fixture.Select("SELECT to_tsvector('turkish','biz kitaplar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'kitap':2"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'kitaplar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('turkish',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('turkish','kitaplar') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('turkish','kitaplar') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			turkishSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION turkish_copy (COPY=turkish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION turkish_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('turkish_copy','biz kitaplar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'biz':1 'kitaplar':2"}}))
			turkishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('turkish','biz kitaplar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'kitap':2"}}))
		}
		check()
		preserved := turkishDefinitionSnapshot(t, p, b, c, "turkish_stem", "turkish_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION turkish_copy ALTER MAPPING FOR asciiword,turkish_missing_alias WITH turkish_stem", "22023", `token type "turkish_missing_alias" does not exist`)
		preserved()
		check()
	})
}
