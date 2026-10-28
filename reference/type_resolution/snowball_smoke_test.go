package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSnowballFeatureSmoke(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RATS') AS lexemes", "{rat}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','ONLY') AS lexemes", "{}")
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY snowball_smoke (TEMPLATE=pg_catalog.snowball,Language=english,StopWords=english)", "CREATE TEXT SEARCH CONFIGURATION snowball_smoke_config (COPY=pg_catalog.english)", "ALTER TEXT SEARCH CONFIGURATION snowball_smoke_config ALTER MAPPING FOR asciiword WITH snowball_smoke", "CREATE TABLE snowball_smoke_refs(d regdictionary DEFAULT 'snowball_smoke'::regdictionary)"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO snowball_smoke_refs DEFAULT VALUES", 1)
		constantSQL := "SELECT ts_lexize('snowball_smoke'::regdictionary,'ONLY'::text) AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constant.Close()
		ch := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		parameterSQL := "SELECT ts_lexize('snowball_smoke'::regdictionary,$1::text) AS lexemes"
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		ph := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		vectorSQL := "SELECT to_tsvector('snowball_smoke_config'::regconfig,$1::text) AS normalized"
		vector, err := c.PrepareContext(p.Ctx, vectorSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer vector.Close()
		vh := fixture.HeldStatement{Executor: c, Statement: vector, SQL: vectorSQL}
		for _, e := range []fixture.Executor{c, vh} {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(vectorSQL, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'fat':2 'rat':3"}}, "The Fat Rats"))
		}
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{}", "ONLY")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY snowball_smoke (stopwords)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, c, constantSQL, "{onli}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{onli}", "ONLY")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY snowball_smoke (stopwords=english,language=snowball_bogus)", "42704", `no Snowball stemmer available for language "snowball_bogus" and encoding "UTF8"`)
		check := func() {
			dictionaryResult(t, p, b, c, constantSQL, "{onli}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='snowball_smoke'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'english'"}})
			regtsSelect(t, p, b, c, "SELECT d='snowball_smoke'::regdictionary AS same FROM snowball_smoke_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(vectorSQL, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'fat':2 'rat':3 'the':1"}}, "The Fat Rats"))
		}
		check()
		constant.Close()
		parameter.Close()
		vector.Close()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted stream required")
			}
			for _, q := range []string{"DROP TABLE snowball_smoke_refs", "DROP TEXT SEARCH CONFIGURATION snowball_smoke_config", "DROP TEXT SEARCH DICTIONARY snowball_smoke"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','ONLY') AS lexemes", "{}")
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check()
		regtsExec(t, p, b, c, "CREATE TABLE snowball_smoke_sentinel(v int)", 0)
		regtsExec(t, p, b, c, "INSERT INTO snowball_smoke_sentinel VALUES(42)", 1)
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		d := dictionaryDiagnostic("22023", `unrecognized Snowball parameter: "accept"`)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(string(emitted)+"ALTER TEXT SEARCH DICTIONARY snowball_smoke (stopwords=english,accept=true);")), &d)
		check()
		regtsSelect(t, p, b, c, "SELECT v FROM snowball_smoke_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("smoke failure published", err)
		}
	})
}
