package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSpanishSnowballPrepared(t *testing.T) {
	t.Run("stock-stopword-lifetime", spanishStockPrepared)
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY spanish_prepared (TEMPLATE=snowball,Language=spanish,StopWords=spanish)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='spanish_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('spanish_prepared'::regdictionary,'más'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE spanish_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE spanish_named", 0)
		regtsExec(t, p, b, c, "PREPARE spanish_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE spanish_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE spanish_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "spanish_prepared", "más")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE spanish_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{mas}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{mas}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{mas}", "spanish_prepared", "más")
			dictionaryResult(t, p, b, c, "EXECUTE spanish_params('spanish_prepared','más')", "{mas}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{cas}", []byte("spanish_prepared"), []byte("casas"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{running}", "spanish_prepared", "RUNNING")
		}
		check()
		preserved := spanishDefinitionSnapshot(t, p, b, c, "spanish_prepared", "spanish")
		spanishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_prepared (language=english,stopwords=spanish_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='spanish_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'spanish'"}})
		check()
		warmSQL := "SELECT ts_lexize('spanish_prepared','casas') AS lexemes"
		warm, err := c.PrepareContext(p.Ctx, warmSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer warm.Close()
		wh := fixture.HeldStatement{Executor: c, Statement: warm, SQL: warmSQL}
		for range 6 {
			dictionaryResult(t, p, b, wh, warmSQL, "{cas}")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{casa}", "spanish_prepared", "casas")
		dictionaryResult(t, p, b, wh, warmSQL, "{cas}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "spanish_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := spanishDefinitionSnapshot(t, p, b, c, "spanish_prepared", "spanish")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_prepared (language=spanish_bogus)", "42704", `no Snowball stemmer available for language "spanish_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "spanish_prepared", "RUNNING")
		dictionaryResult(t, p, b, ph, parameterSQL, "{casa}", "spanish_prepared", "casas")
		dictionaryResult(t, p, b, wh, warmSQL, "{cas}")
	})
}

func spanishStockPrepared(t *testing.T) {
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer spanishStockCleanup(t, p, b, c)()
		const constantSQL = "SELECT ts_lexize('spanish_stem','más') AS lexemes"
		const dynamicSQL = "SELECT ts_lexize('spanish_stem',$1::text) AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constant.Close()
		dynamic, err := c.PrepareContext(p.Ctx, dynamicSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer dynamic.Close()
		ch := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		dh := fixture.HeldStatement{Executor: c, Statement: dynamic, SQL: dynamicSQL}
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, dh, dynamicSQL, "{}", "más")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY spanish_stem (stopwords)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, dh, dynamicSQL, "{mas}", "más")
		dictionaryResult(t, p, b, c, constantSQL, "{mas}")
		fresh, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{mas}")
	})
}
