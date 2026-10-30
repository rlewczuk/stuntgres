package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEstonianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY estonian_prepared (TEMPLATE=snowball,Language=estonian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='estonian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('estonian_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE estonian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE estonian_named", 0)
		regtsExec(t, p, b, c, "PREPARE estonian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE estonian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE estonian_named", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "estonian_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_prepared (stopwords=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "estonian_prepared", "RATS")
			dictionaryResult(t, p, b, ph, parameterSQL, "{raama}", "estonian_prepared", "raamatud")
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE estonian_named", "{only}")
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "estonian_prepared", "ONLY")
			dictionaryResult(t, p, b, c, "EXECUTE estonian_params('estonian_prepared','ONLY')", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", []byte("estonian_prepared"), []byte("ONLY"))
		}
		check()
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_prepared (stopwords=english,language=estonian_bogus)", "42704", `no Snowball stemmer available for language "estonian_bogus" and encoding "UTF8"`)
		estonianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_prepared (language=estonian,stopwords=estonian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='estonian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'estonian', stopwords = 'english'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY estonian_prepared (stopwords)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{raama}", "estonian_prepared", "RAAMATUD")
	})
}
