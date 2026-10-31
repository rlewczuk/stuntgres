package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceNorwegianSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"BILER", "{bil}"}, {"båtene", "{båt}"}, {"ikke", "{}"}, {"PÅ", "{pÅ}"}} {
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('norwegian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		norwegianSmokeBoth(t, p, b, c, norwegianDebugStep())
		norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('norwegian','og biler på båtene') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'bil':2 'båt':4"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY norwegian_smoke (TEMPLATE=snowball,Language='NoRwEgIaN')", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION norwegian_smoke_config (COPY=norwegian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION norwegian_smoke_config ALTER MAPPING FOR asciiword WITH simple", 0)
		check := func() {
			for _, tc := range [][2]string{{"ikke", "{ikk}"}, {"running", "{running}"}} {
				norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('norwegian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
			}
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('norwegian_smoke_config','og biler på båtene') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'biler':2 'båt':4 'og':1"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='norwegian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'NoRwEgIaN'"}})
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('norwegian_stem','ikke') AS lexemes", "{}")
		}
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
