package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceItalianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY italian_prepared (TEMPLATE=snowball,Language=italian,StopWords=italian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='italian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('italian_prepared'::regdictionary,'perché'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE italian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE italian_named", 0)
		regtsExec(t, p, b, c, "PREPARE italian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE italian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE italian_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "italian_prepared", "perché")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE italian_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{perc}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{perc}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{perc}", "italian_prepared", "perché")
			dictionaryResult(t, p, b, c, "EXECUTE italian_params('italian_prepared','perché')", "{perc}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{can}", []byte("italian_prepared"), []byte("cani"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "italian_prepared", "RATS")
		}
		check()
		italianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_prepared (language=english,stopwords=italian_missing)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='italian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'italian'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "italian_prepared", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY italian_prepared (language=italian_bogus)", "42704", `no Snowball stemmer available for language "italian_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "italian_prepared", "RATS")
	})
}
