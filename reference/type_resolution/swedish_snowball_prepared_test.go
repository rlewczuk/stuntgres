package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSwedishSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY swedish_prepared (TEMPLATE=snowball,Language=swedish,StopWords=swedish)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='swedish_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('swedish_prepared'::regdictionary,'sitta'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE swedish_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE swedish_named", 0)
		regtsExec(t, p, b, c, "PREPARE swedish_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE swedish_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE swedish_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "swedish_prepared", "sitta")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE swedish_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{sitt}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{sitt}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{sitt}", "swedish_prepared", "sitta")
			dictionaryResult(t, p, b, c, "EXECUTE swedish_params('swedish_prepared','sitta')", "{sitt}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{hund}", []byte("swedish_prepared"), []byte("hundar"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{running}", "swedish_prepared", "RUNNING")
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "swedish_prepared", "swedish")
		swedishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_prepared (language=english,stopwords=swedish_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='swedish_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'swedish'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "swedish_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := norwegianDefinitionSnapshot(t, p, b, c, "swedish_prepared", "swedish")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY swedish_prepared (language=swedish_bogus)", "42704", `no Snowball stemmer available for language "swedish_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "swedish_prepared", "RUNNING")
	})
}
