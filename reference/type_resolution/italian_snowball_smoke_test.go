package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceItalianSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"CANI", "{can}"}, {"divano", "{divan}"}, {"perché", "{}"}, {"piú", "{più}"}} {
			italianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('italian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		italianSmokeBoth(t, p, b, c, italianDebugStep())
		italianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('italian','i gatti e i cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'can':5 'gatt':2"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY italian_smoke (TEMPLATE=snowball,Language='ItAlIaN',StopWords=italian)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION italian_smoke_config (COPY=italian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION italian_smoke_config ALTER MAPPING FOR asciiword,word WITH italian_smoke", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_smoke (stopwords)", 0)
		check := func() {
			italianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('italian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{perc}"}}, "perché"))
			italianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('italian_smoke_config','perché cani') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'can':2 'perc':1"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='italian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'ItAlIaN'"}})
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('italian_smoke','cani') AS lexemes", "{can}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('italian_stem','perché') AS lexemes", "{}")
		}
		check()
		italianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_smoke (language=english,stopwords=italian_missing)")
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
