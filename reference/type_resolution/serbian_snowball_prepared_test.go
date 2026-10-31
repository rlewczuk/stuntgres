package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSerbianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY serbian_prepared (TEMPLATE=snowball,Language=serbian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='serbian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		const constantSQL = "SELECT ts_lexize('serbian_prepared'::regdictionary,'ONLY'::text) AS lexemes"
		const parameterSQL = "SELECT ts_lexize($1,$2) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE serbian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE serbian_named", 0)
		regtsExec(t, p, b, c, "PREPARE serbian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE serbian_params", 0)
		regtsExec(t, p, b, c, "PREPARE serbian_inferred AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE serbian_inferred", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE serbian_named", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "serbian_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_prepared (stopwords=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE serbian_named", "{only}")
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
			fresh.Close()
			for _, input := range []any{"ONLY", []byte("ONLY")} {
				dictionaryResult(t, p, b, ph, parameterSQL, "{}", "serbian_prepared", input)
			}
			dictionaryResult(t, p, b, c, "EXECUTE serbian_params('serbian_prepared','ONLY')", "{}")
			dictionaryResult(t, p, b, c, "EXECUTE serbian_inferred('serbian_prepared','рад')", "{rad}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{rad}", []byte("serbian_prepared"), []byte("рад"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{abakus}", "serbian_prepared", "ABAKUSA")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "serbian_stem", "ONLY")
		}
		check()
		serbianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_prepared (language=english,stopwords=serbian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='serbian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'serbian', stopwords = 'english'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "serbian_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{only}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY serbian_prepared (language=serbian_bogus)", "42704", `no Snowball stemmer available for language "serbian_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "serbian_prepared", "RUNNING")
	})
}
