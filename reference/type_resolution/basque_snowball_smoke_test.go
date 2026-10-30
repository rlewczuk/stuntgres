package reference_test

import (
	"bytes"
	"database/sql"
	"path"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func basqueMissingStopwords(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic("F0000", `could not open stop-word file "basque.stop": No such file or directory`))
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "`+path.Join(fixture.TextSearchDataDirectory, "basque.stop")+`": No such file or directory`)
		s.PostgresDiagnostic = &pg
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func basqueSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}
	for range 2 {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceBasqueSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stock := "SELECT ts_lexize('basque_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, stock)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"etxeak", "etxe"}, {"RATS", "rats"}, {"ONLY", "only"}, {"eta", "eta"}, {"bababatuatseden", "bababatseden"}, {"minutuko", "minutu"}} {
			dictionaryResult(t, p, b, c, stock, "{"+tc[1]+"}", tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: stock}, stock, "{"+tc[1]+"}", tc[0])
		}
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='basque_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'basque'"}})
		regtsSelect(t, p, b, c, "SELECT mapseqno,mapdict='basque_stem'::regdictionary::oid AS basque FROM pg_ts_config_map WHERE mapcfg='basque'::regconfig AND maptokentype=1", []string{"mapseqno", "basque"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY basque_smoke (TEMPLATE=snowball,Language='BaSqUe',StopWords=english)", "CREATE TEXT SEARCH CONFIGURATION basque_smoke_config (COPY=basque)", "CREATE TABLE basque_smoke_refs(d regdictionary DEFAULT 'basque_smoke'::regdictionary)"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO basque_smoke_refs DEFAULT VALUES", 1)
		for _, tc := range []struct{ input, want string }{{"ONLY", "{}"}, {"etxeak", "{etxe}"}} {
			basqueSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('basque_smoke',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input))
		}
		basqueSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('basque','etxeak etxeko RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'etxe':1 'etxeko':2 'rats':3"}}))
		basqueSmokeBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_debug('basque','etxeak RATS')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "etxeak", "{basque_stem}", "basque_stem", "{etxe}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "RATS", "{basque_stem}", "basque_stem", "{rats}"}}))
		constantSQL := "SELECT ts_lexize('basque_smoke','RATS') AS lexemes"
		parameterSQL := "SELECT ts_lexize('basque_smoke',$1::text) AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constant.Close()
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		ch := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		ph := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		dictionaryResult(t, p, b, ch, constantSQL, "{rats}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "RATS")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY basque_smoke (language=english)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{rats}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "RATS")
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'basque_smoke'::regdictionary::oid::bigint").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		basqueMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY basque_smoke (language=basque,stopwords=basque)")
		regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='basque_smoke'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{identity, "stopwords = 'english', language = 'english'"}})
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{rats}")
		check := func() {
			dictionaryResult(t, p, b, c, constantSQL, "{rats}")
			dictionaryResult(t, p, b, c, parameterSQL, "{etxe}", "etxeak")
			dictionaryResult(t, p, b, c, parameterSQL, "{}", "ONLY")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='basque_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"stopwords = 'english', language = 'BaSqUe'"}})
			regtsSelect(t, p, b, c, "SELECT d='basque_smoke'::regdictionary AS same FROM basque_smoke_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
			dictionaryResult(t, p, b, c, stock, "{etxe}", "etxeak")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY basque_smoke (language='BaSqUe')", 0)
		check()
		stmt.Close()
		constant.Close()
		parameter.Close()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted SQL required")
			}
			for _, q := range []string{"DROP TABLE basque_smoke_refs", "DROP TEXT SEARCH CONFIGURATION basque_smoke_config", "DROP TEXT SEARCH DICTIONARY basque_smoke"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native SQL replay", err)
			}
			check()
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		dictionaryResult(t, p, b, c, stock, "{only}", "ONLY")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='basque_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'basque'"}})
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check()
	})
}
