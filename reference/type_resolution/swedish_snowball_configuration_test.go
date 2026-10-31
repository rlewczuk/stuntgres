package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func swedishDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('swedish','HUNDAR')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "HUNDAR", "{swedish_stem}", "swedish_stem", "{hund}"}})
}

func TestReferenceSwedishSnowballConfiguration(t *testing.T) {
	t.Run("configuration-conversion", swedishConfigurationSettings)
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
				dict = "swedish_stem"
			}
			maps = append(maps, []any{id, int64(1), dict, true})
		}
		norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT tokid::bigint,alias FROM ts_token_type('default') ORDER BY tokid", []string{"tokid", "alias"}, []string{"INT8", "TEXT"}, tokens))
		q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict::regdictionary::text AS dictionary,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN 'swedish_stem'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='swedish'::regconfig ORDER BY maptokentype,mapseqno"
		norwegianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"maptokentype", "mapseqno", "dictionary", "same"}, []string{"INT8", "INT8", "TEXT", "BOOL"}, maps))
		norwegianSmokeBoth(t, p, b, c, swedishDebugStep())
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM ts_debug('swedish','hästar')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"word", "Word, all letters", "hästar", "{swedish_stem}", "swedish_stem", "{häst}"}}),
			fixture.Select("SELECT to_tsvector('swedish','och hundar på hästar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hund':2 'häst':4"}}),
			fixture.Select("SELECT to_tsvector(NULL::regconfig,'hundar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('swedish',NULL::text) AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{nil}}),
			fixture.Select("SELECT to_tsvector('swedish','hundar') AS normalized WHERE false", []string{"normalized"}, []string{"TSVECTOR"}, nil),
			fixture.Select("SELECT * FROM ts_debug('swedish','hundar') WHERE false", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, nil),
		} {
			norwegianSmokeBoth(t, p, b, c, s)
		}
		const parameterSQL = "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		for _, tc := range []struct {
			config, document any
			want             any
		}{
			{"swedish", "och hundar på hästar", "'hund':2 'häst':4"},
			{[]byte("swedish"), []byte("HUNDAR"), "'hund':1"},
			{"swedish", nil, nil},
			{nil, "hundar", nil},
		} {
			s := fixture.Select(parameterSQL, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.config, tc.document)
			for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}} {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			}
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION swedish_copy (COPY=swedish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION swedish_copy ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('swedish_copy','och hundar på hästar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hundar':2 'häst':4 'och':1"}}))
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('swedish','och hundar på hästar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hund':2 'häst':4"}}))
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "swedish_stem", "swedish_copy")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION swedish_copy ALTER MAPPING FOR asciiword,swedish_missing_alias WITH swedish_stem", "22023", `token type "swedish_missing_alias" does not exist`)
		preserved()
		check()
	})
}
