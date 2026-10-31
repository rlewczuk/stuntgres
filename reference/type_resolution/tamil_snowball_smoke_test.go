package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func tamilMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "tamil.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "tamil.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceTamilSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"மரத்தில்", "மரம்"}, {"புத்தகங்கள்", "புத்தகம்"}, {"வீடுகள்", "வீடு"}, {"RUNNING", "running"}, {"ONLY", "only"}} {
			tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('tamil_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0]))
		}
		tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('tamil','THE மரத்தில் மரம் RUNNING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'running':4 'the':1 'மரம்':2,3"}}))
		tamilSmokeBoth(t, p, b, c, tamilDebugStep())
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='tamil_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'tamil'"}})
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY tamil_smoke (TEMPLATE=snowball,Language=tamil,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION tamil_smoke_config (COPY=tamil)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION tamil_smoke_config ALTER MAPPING FOR asciiword,word WITH tamil_smoke", 0)
		check := func(only, options, vector string) {
			tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('tamil_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{only}}, "ONLY"))
			tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('tamil_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{தமிழ்}"}}, "தமிழில்"))
			tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('tamil_smoke_config','ONLY தமிழில்') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{vector}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='tamil_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}})
		}
		check("{}", "language = 'tamil', stopwords = 'english'", "'தமிழ்':2")
		preserved := tamilDefinitionSnapshot(t, p, b, c, "tamil_smoke", "tamil_smoke_config")
		tamilMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_smoke (language=english,stopwords=tamil)")
		preserved()
		check("{}", "language = 'tamil', stopwords = 'english'", "'தமிழ்':2")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_smoke (stopwords)", 0)
		check("{only}", "language = 'tamil'", "'only':1 'தமிழ்':2")
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('tamil_stem','ONLY') AS lexemes", "{only}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='tamil_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'tamil'"}})
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check("{only}", "language = 'tamil'", "'only':1 'தமிழ்':2")
		}
	})
}