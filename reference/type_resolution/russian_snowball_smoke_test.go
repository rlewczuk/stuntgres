package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRussianSnowballFeatureSmoke(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"машины", "{машин}"}, {"МАШИНЫ", "{МАШИНЫ}"}, {"все", "{}"}, {"всё", "{все}"}, {"RUNNING", "{running}"}} {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('russian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('russian','THE машины и RUNNING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'run':4 'машин':2"}}))
		portugueseSmokeBoth(t, p, b, c, russianDebugStep())
		for _, q := range []string{
			"CREATE TEXT SEARCH DICTIONARY russian_smoke (TEMPLATE=snowball,Language=russian,StopWords=russian)",
			"CREATE TEXT SEARCH CONFIGURATION russian_smoke_config (COPY=russian)",
			"ALTER TEXT SEARCH CONFIGURATION russian_smoke_config ALTER MAPPING FOR word,hword_part,hword WITH russian_smoke",
			"ALTER TEXT SEARCH DICTIONARY russian_smoke (stopwords)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		check := func() {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('russian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{все}"}}, "все"))
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('russian_smoke_config','все машины RUNNING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'run':3 'все':1 'машин':2"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='russian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'russian'"}})
		}
		check()
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "russian_smoke", "russian_smoke_config")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_smoke (stopwords=russian,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		preserved()
		check()
		russianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_smoke (language=english,stopwords=russian_missing)")
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
