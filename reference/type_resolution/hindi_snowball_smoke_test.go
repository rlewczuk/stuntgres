package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hindiMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "hindi.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "hindi.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceHindiSnowballFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"करती", "कर"}, {"RATS", "rats"}, {"में", "म"}} {
			hindiSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hindi_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0]))
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY hindi_smoke (TEMPLATE=snowball,Language=hindi,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION hindi_smoke_config (COPY=hindi)", 0)
		hindiSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hindi_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{}"}}, "ONLY"))
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hindi_smoke (stopwords)", 0)
		check := func() {
			hindiSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('hindi_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{only}"}}, "ONLY"))
			hindiSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('hindi_smoke_config','THE RATS कमल') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rat':2 'कमल':3"}}))
			hindiSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('hindi','RATS कमल')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "RATS", "{english_stem}", "english_stem", "{rat}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"word", "Word, all letters", "कमल", "{hindi_stem}", "hindi_stem", "{कमल}"}}))
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='hindi_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'hindi'"}})
		}
		check()
		hindiMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hindi_smoke (stopwords=hindi)")
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
