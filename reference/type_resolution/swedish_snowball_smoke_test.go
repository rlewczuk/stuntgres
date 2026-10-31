package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSwedishSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"HUNDAR", "{hund}"}, {"hästar", "{häst}"}, {"sitta", "{}"}, {"PÅ", "{pÅ}"}} {
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('swedish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		norwegianSmokeBoth(t, p, b, c, swedishDebugStep())
		norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('swedish','och hundar på hästar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hund':2 'häst':4"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY swedish_smoke (TEMPLATE=snowball,Language='SwEdIsH')", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION swedish_smoke_config (COPY=swedish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION swedish_smoke_config ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			for _, tc := range [][2]string{{"sitta", "{sitt}"}, {"running", "{running}"}} {
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('swedish_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('swedish_smoke_config','och hundar på hästar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hundar':2 'häst':4 'och':1"}}))
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('swedish','och hundar på hästar') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'hund':2 'häst':4"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='swedish_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'SwEdIsH'"}})
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('swedish_stem','sitta') AS lexemes", "{}")
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "swedish_smoke", "swedish_smoke_config")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_smoke (language=english,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		preserved()
		check()
		swedishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_smoke (language=english,stopwords=swedish_missing)")
		preserved()
		check()
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
