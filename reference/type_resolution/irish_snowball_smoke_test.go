package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func irishMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "irish.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "irish.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceIrishSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"mbababaire", "babab"}, {"abhcóideacht", "abhcóid"}, {"RATS", "rats"}, {"ONLY", "only"}} {
			irishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('irish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0]))
		}
		irishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('irish','RATS abhcóideacht agus') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abhcóid':2 'agus':3 'rats':1"}}))
		irishSmokeBoth(t, p, b, c, irishDebugStep())
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY irish_smoke (TEMPLATE=snowball,Language=irish,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION irish_smoke_config (COPY=irish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION irish_smoke_config ALTER MAPPING FOR asciiword,word WITH irish_smoke", 0)
		check := func(only, options, vector string) {
			irishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('irish_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{only}}, "ONLY"))
			irishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('irish_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{abhcóid}"}}, "abhcóideacht"))
			irishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('irish_smoke_config','ONLY abhcóideacht') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{vector}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='irish_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}})
		}
		check("{}", "language = 'irish', stopwords = 'english'", "'abhcóid':2")
		preserved := irishDefinitionSnapshot(t, p, b, c, "irish_smoke", "irish_smoke_config")
		irishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_smoke (language=english,stopwords=irish)")
		preserved()
		check("{}", "language = 'irish', stopwords = 'english'", "'abhcóid':2")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_smoke (stopwords)", 0)
		check("{only}", "language = 'irish'", "'abhcóid':2 'only':1")
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('irish_stem','ONLY') AS lexemes", "{only}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='irish_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'irish'"}})
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check("{only}", "language = 'irish'", "'abhcóid':2 'only':1")
		}
	})
}
