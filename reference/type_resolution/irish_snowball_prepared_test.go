package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIrishSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY irish_prepared (TEMPLATE=snowball,Language=irish,StopWords=english)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='irish_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('irish_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE irish_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE irish_named", 0)
		regtsExec(t, p, b, c, "PREPARE irish_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE irish_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE irish_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "irish_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE irish_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{only}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{only}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "irish_prepared", "ONLY")
			dictionaryResult(t, p, b, c, "EXECUTE irish_params('irish_prepared','ONLY')", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{abhcóid}", []byte("irish_prepared"), []byte("abhcóideacht"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "irish_prepared", "RATS")
		}
		check()
		irishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_prepared (language=english,stopwords=irish)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='irish_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'irish'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "irish_prepared", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_prepared (language=irish_bogus)", "42704", `no Snowball stemmer available for language "irish_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "irish_prepared", "RATS")
	})
}
