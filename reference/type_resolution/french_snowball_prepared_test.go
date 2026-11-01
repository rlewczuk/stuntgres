package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFrenchSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY french_prepared (TEMPLATE=snowball,Language=french,StopWords=french)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='french_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('french_prepared'::regdictionary,'été'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE french_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE french_named", 0)
		regtsExec(t, p, b, c, "PREPARE french_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE french_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE french_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "french_prepared", "été")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ph, parameterSQL, "{chat}", "french_prepared", "CHATS")
			dictionaryResult(t, p, b, ph, parameterSQL, "{cheval}", "french_prepared", "chevaux")
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE french_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{été}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{été}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{été}", "french_prepared", "été")
			dictionaryResult(t, p, b, c, "EXECUTE french_params('french_prepared','été')", "{été}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{été}", []byte("french_prepared"), []byte("été"))
		}
		check()
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_prepared (stopwords=english,language=french_bogus)", "42704", `no Snowball stemmer available for language "french_bogus" and encoding "UTF8"`)
		frenchMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_prepared (language=french,stopwords=french_missing)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='french_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'french'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY french_prepared (stopwords)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{cheval}", "french_prepared", "CHEVAUX")
	})
}
