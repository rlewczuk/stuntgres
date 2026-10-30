package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceHungarianSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{
			{"ház!", "{ház!}"}, {"házak!", "{házak!}"}, {"házak ", `{"házak "}`}, {" házak ", `{" házak "}`},
			{"a\u0301", "{a\u0301}"},
			{"b" + strings.Repeat("a", 998) + "t", "{b" + strings.Repeat("a", 996) + "}"},
			{"b" + strings.Repeat("a", 999) + "t", "{b" + strings.Repeat("a", 999) + "t}"},
			{"b" + strings.Repeat("a", 996) + "át", "{b" + strings.Repeat("a", 996) + "}"},
			{"b" + strings.Repeat("a", 997) + "át", "{b" + strings.Repeat("a", 997) + "át}"},
			{"B" + strings.Repeat("A", 997) + "át", "{b" + strings.Repeat("a", 997) + "át}"},
		} {
			hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hungarian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
	})
}

func hungarianDebugStep() fixture.Step {
	return fixture.Select("SELECT * FROM ts_debug('hungarian','a házak')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{
		{"asciiword", "Word, all ASCII", "a", "{hungarian_stem}", "hungarian_stem", "{}"},
		{"blank", "Space symbols", " ", "{}", nil, nil},
		{"word", "Word, all letters", "házak", "{hungarian_stem}", "hungarian_stem", "{ház}"},
	})
}

func TestReferenceHungarianSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"házak", "{ház}"}, {"HÁZAK", "{hÁzak}"}, {"cikk", "{}"}, {"cikkben", "{cik}"}, {"RATS", "{rats}"}} {
			hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hungarian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
		hungarianSmokeBoth(t, p, b, c, hungarianDebugStep())
		hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('hungarian','a házak és házban') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ház':2,4"}}))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY hungarian_smoke (TEMPLATE=snowball,Language=hungarian,StopWords=hungarian)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION hungarian_smoke_config (COPY=hungarian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION hungarian_smoke_config ALTER MAPPING FOR asciiword,word WITH hungarian_smoke", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hungarian_smoke (stopwords)", 0)
		check := func() {
			hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hungarian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{cik}"}}, "cikk"))
			hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('hungarian_smoke_config','cikk házak') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cik':1 'ház':2"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='hungarian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'hungarian'"}})
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('hungarian_smoke','házak') AS lexemes", "{ház}")
		}
		check()
		hungarianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hungarian_smoke (language=english,stopwords=hungarian_missing)")
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
