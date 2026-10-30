package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIndonesianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY indonesian_prepared (TEMPLATE=snowball,Language=indonesian,StopWords=english)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='indonesian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('indonesian_prepared'::regdictionary,'THE'::text) AS lexemes"
		parameterSQL := "SELECT ts_lexize($1,$2) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE indonesian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE indonesian_named", 0)
		regtsExec(t, p, b, c, "PREPARE indonesian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE indonesian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE indonesian_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "indonesian_prepared", "THE")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE indonesian_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{the}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{the}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{the}", "indonesian_prepared", "THE")
			dictionaryResult(t, p, b, c, "EXECUTE indonesian_params('indonesian_prepared','THE')", "{the}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{sapu}", []byte("indonesian_prepared"), []byte("MENYAPU"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "indonesian_prepared", "RATS")
		}
		check()
		indonesianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_prepared (language=english,stopwords=indonesian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='indonesian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'indonesian'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "indonesian_prepared", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY indonesian_prepared (language=indonesian_bogus)", "42704", `no Snowball stemmer available for language "indonesian_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "indonesian_prepared", "RATS")
	})
}
