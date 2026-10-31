package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func serbianMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "serbian.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "serbian.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceSerbianSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"ABAKUSA", "abakus"}, {"рад", "rad"}, {"acetat", "aceta"}, {"acetata", "acetat"}, {"THE", "the"}, {"ONLY", "only"}} {
			serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('serbian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0]))
		}
		serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('serbian','ABAKUSA рад THE') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abakus':1 'rad':2 'the':3"}}))
		serbianSmokeBoth(t, p, b, c, serbianDebugStep())
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY serbian_smoke (TEMPLATE=snowball,Language=serbian,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION serbian_smoke_config (COPY=serbian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION serbian_smoke_config ALTER MAPPING FOR asciiword,word WITH serbian_smoke", 0)
		check := func(only, options, vector string) {
			serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('serbian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{only}}, "ONLY"))
			serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('serbian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{rad}"}}, "рад"))
			serbianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('serbian_smoke_config','ONLY abakusa') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{vector}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='serbian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}})
		}
		check("{}", "language = 'serbian', stopwords = 'english'", "'abakus':2")
		preserved := serbianDefinitionSnapshot(t, p, b, c, "serbian_smoke", "serbian_smoke_config")
		serbianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_smoke (language=english,stopwords=serbian)")
		preserved()
		check("{}", "language = 'serbian', stopwords = 'english'", "'abakus':2")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_smoke (stopwords)", 0)
		check("{only}", "language = 'serbian'", "'abakus':2 'only':1")
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('serbian_stem','ONLY') AS lexemes", "{only}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='serbian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'serbian'"}})
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check("{only}", "language = 'serbian'", "'abakus':2 'only':1")
		}
	})
}