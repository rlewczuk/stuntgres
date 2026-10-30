package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGreekSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY greek_prepared (TEMPLATE=snowball,Language=greek,StopWords=english)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='greek_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('greek_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE greek_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE greek_named", 0)
		regtsExec(t, p, b, c, "PREPARE greek_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE greek_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE greek_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "greek_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE greek_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{only}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{only}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "greek_prepared", "ONLY")
			dictionaryResult(t, p, b, c, "EXECUTE greek_params('greek_prepared','ONLY')", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{καλ}", []byte("greek_prepared"), []byte("ΚΑΛΌΣ"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "greek_prepared", "RATS")
		}
		check()
		greekMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY greek_prepared (language=english,stopwords=greek)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='greek_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'greek'"}})
		check()
	})
}
