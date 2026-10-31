package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func lithuanianMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "lithuanian.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "lithuanian.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceLithuanianSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"sesers", "ses"}, {"seseris", "seser"}, {"parking'as", "parking'"}, {"gervė", "gerv"}, {"VYRAS", "vyr"}, {"ONLY", "onl"}} {
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('lithuanian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0]))
		}
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('lithuanian','VYRAS gervė ir') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gerv':2 'ir':3 'vyr':1"}}))
		lithuanianSmokeBoth(t, p, b, c, lithuanianDebugStep())
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY lithuanian_smoke (TEMPLATE=snowball,Language=lithuanian,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION lithuanian_smoke_config (COPY=lithuanian)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION lithuanian_smoke_config ALTER MAPPING FOR asciiword,word WITH lithuanian_smoke", 0)
		check := func(only, options, vector string) {
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('lithuanian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{only}}, "ONLY"))
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('lithuanian_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{gerv}"}}, "gervė"))
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('lithuanian_smoke_config','ONLY vyras') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{vector}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='lithuanian_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}})
		}
		check("{}", "language = 'lithuanian', stopwords = 'english'", "'vyr':2")
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "lithuanian_smoke", "lithuanian_smoke_config")
		lithuanianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY lithuanian_smoke (language=english,stopwords=lithuanian)")
		preserved()
		check("{}", "language = 'lithuanian', stopwords = 'english'", "'vyr':2")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY lithuanian_smoke (stopwords)", 0)
		check("{onl}", "language = 'lithuanian'", "'onl':1 'vyr':2")
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('lithuanian_stem','ONLY') AS lexemes", "{onl}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='lithuanian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'lithuanian'"}})
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check("{onl}", "language = 'lithuanian'", "'onl':1 'vyr':2")
		}
	})
}