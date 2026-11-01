package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func russianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('russian','RATS машины')",
		[]string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"},
		[]string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"},
		[][]any{
			{"asciiword", "Word, all ASCII", "RATS", "{english_stem}", "english_stem", "{rat}"},
			{"blank", "Space symbols", " ", "{}", nil, nil},
			{"word", "Word, all letters", "машины", "{russian_stem}", "russian_stem", "{машин}"},
		})
}

func TestReferenceRussianSnowballConfiguration(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT * FROM ts_debug($1,$2)", "SELECT * FROM ts_debug($1::regconfig,$2::text)"} {
			s := russianDebugStep()
			s.Query, s.Args = q, []any{"russian", "RATS машины"}
			portugueseSmokeBoth(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT to_tsvector($1,$2) AS normalized", "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"} {
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"russian", "THE машины и RUNNING"}, "'run':4 'машин':2"},
				{[]any{[]byte("russian"), []byte("THE машины и RUNNING")}, "'run':4 'машин':2"},
				{[]any{nil, "машины"}, nil}, {[]any{"russian", nil}, nil},
			} {
				portugueseSmokeBoth(t, p, b, c, fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.args...))
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
			case 1, 11, 16:
				dict = "english_stem"
			case 2, 10, 17:
				dict = "russian_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,11,16) THEN 'english_stem'::regdictionary::oid WHEN maptokentype IN (2,10,17) THEN 'russian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='russian'::regconfig ORDER BY maptokentype,mapseqno"
		portugueseSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		for _, tc := range []struct{ token, full, part, description, dict, stem, fullStem string }{
			{"RATS-RATS", "asciihword", "hword_asciipart", "all ASCII", "english_stem", "rat", "rats-rat"},
			{"машины-машины", "hword", "hword_part", "all letters", "russian_stem", "машин", "машины-машин"},
		} {
			part := "RATS"
			if tc.dict == "russian_stem" {
				part = "машины"
			}
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('russian',$1::text) WHERE alias<>'blank'", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
				{tc.full, "Hyphenated word, " + tc.description, tc.token, "{" + tc.dict + "}", tc.dict, "{" + tc.fullStem + "}"},
				{tc.part, "Hyphenated word part, " + tc.description, part, "{" + tc.dict + "}", tc.dict, "{" + tc.stem + "}"},
				{tc.part, "Hyphenated word part, " + tc.description, part, "{" + tc.dict + "}", tc.dict, "{" + tc.stem + "}"},
			}, tc.token))
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION russian_copy (COPY=russian)", 0)
		copyMaps := fixture.Select("SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,11,16) THEN 'english_stem'::regdictionary::oid WHEN maptokentype IN (2,10,17) THEN 'russian_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='russian_copy'::regconfig ORDER BY maptokentype,mapseqno", []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps)
		portugueseSmokeBoth(t, p, b, c, copyMaps)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION russian_copy ALTER MAPPING FOR asciiword WITH russian_stem", 0)
		check := func() {
			for _, tc := range [][2]string{{"russian", "'run':1 'машин':2"}, {"russian_copy", "'running':1 'машин':2"}} {
				portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector($1::regconfig,'RUNNING машины') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc[1]}}, tc[0]))
			}
		}
		check()
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "russian_stem", "russian_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION russian_copy ALTER MAPPING FOR asciiword,russian_missing_alias WITH simple", "22023", `token type "russian_missing_alias" does not exist`)
		preserved()
		check()
	})
}
