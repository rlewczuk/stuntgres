package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNorwegianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY norwegian_prepared (TEMPLATE=snowball,Language=norwegian,StopWords=norwegian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='norwegian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('norwegian_prepared'::regdictionary,'ikke'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE norwegian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE norwegian_named", 0)
		regtsExec(t, p, b, c, "PREPARE norwegian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE norwegian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE norwegian_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "norwegian_prepared", "ikke")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE norwegian_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{ikk}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{ikk}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{ikk}", "norwegian_prepared", "ikke")
			dictionaryResult(t, p, b, c, "EXECUTE norwegian_params('norwegian_prepared','ikke')", "{ikk}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{bil}", []byte("norwegian_prepared"), []byte("biler"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{running}", "norwegian_prepared", "RUNNING")
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "norwegian_prepared", "norwegian")
		norwegianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_prepared (language=english,stopwords=norwegian_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='norwegian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'norwegian'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "norwegian_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := norwegianDefinitionSnapshot(t, p, b, c, "norwegian_prepared", "norwegian")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY norwegian_prepared (language=norwegian_bogus)", "42704", `no Snowball stemmer available for language "norwegian_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "norwegian_prepared", "RUNNING")
	})
}
