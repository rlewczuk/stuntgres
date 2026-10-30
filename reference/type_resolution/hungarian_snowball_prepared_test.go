package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHungarianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY hungarian_prepared (TEMPLATE=snowball,Language=hungarian,StopWords=hungarian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='hungarian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('hungarian_prepared'::regdictionary,'cikk'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE hungarian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE hungarian_named", 0)
		regtsExec(t, p, b, c, "PREPARE hungarian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE hungarian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE hungarian_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "hungarian_prepared", "cikk")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hungarian_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE hungarian_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{cik}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{cik}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{cik}", "hungarian_prepared", "cikk")
			dictionaryResult(t, p, b, c, "EXECUTE hungarian_params('hungarian_prepared','cikk')", "{cik}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{ház}", []byte("hungarian_prepared"), []byte("házak"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "hungarian_prepared", "RATS")
		}
		check()
		hungarianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hungarian_prepared (language=english,stopwords=hungarian_missing)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='hungarian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'hungarian'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hungarian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "hungarian_prepared", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hungarian_prepared (language=hungarian_bogus)", "42704", `no Snowball stemmer available for language "hungarian_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "hungarian_prepared", "RATS")
	})
}
