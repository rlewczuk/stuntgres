package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHindiSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY hindi_prepared (TEMPLATE=snowball,Language=hindi,StopWords=english)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='hindi_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('hindi_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE hindi_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE hindi_named", 0)
		regtsExec(t, p, b, c, "PREPARE hindi_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE hindi_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE hindi_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "hindi_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hindi_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE hindi_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{only}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{only}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "hindi_prepared", "ONLY")
			dictionaryResult(t, p, b, c, "EXECUTE hindi_params('hindi_prepared','ONLY')", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{कर}", []byte("hindi_prepared"), []byte("करती"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "hindi_prepared", "RATS")
		}
		check()
		hindiMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hindi_prepared (language=english,stopwords=hindi)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='hindi_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'hindi'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hindi_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "hindi_prepared", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY hindi_prepared (language=hindi_bogus)", "42704", `no Snowball stemmer available for language "hindi_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "hindi_prepared", "RATS")
	})
}
