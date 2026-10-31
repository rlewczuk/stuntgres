package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePortugueseSnowballPrepared(t *testing.T) {
	t.Run("stock-stopword-lifetime", portugueseStockPrepared)
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY portuguese_prepared (TEMPLATE=snowball,Language=portuguese,StopWords=portuguese)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='portuguese_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('portuguese_prepared'::regdictionary,'não'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE portuguese_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE portuguese_named", 0)
		regtsExec(t, p, b, c, "PREPARE portuguese_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE portuguese_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE portuguese_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "portuguese_prepared", "não")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE portuguese_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{nã}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{nã}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{nã}", "portuguese_prepared", "não")
			dictionaryResult(t, p, b, c, "EXECUTE portuguese_params('portuguese_prepared','não')", "{nã}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{cas}", []byte("portuguese_prepared"), []byte("casas"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{running}", "portuguese_prepared", "RUNNING")
		}
		check()
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "portuguese_prepared", "portuguese")
		portugueseMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_prepared (language=english,stopwords=portuguese_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='portuguese_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'portuguese'"}})
		check()
		warmSQL := "SELECT ts_lexize('portuguese_prepared','casas') AS lexemes"
		warm, err := c.PrepareContext(p.Ctx, warmSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer warm.Close()
		wh := fixture.HeldStatement{Executor: c, Statement: warm, SQL: warmSQL}
		for range 6 {
			dictionaryResult(t, p, b, wh, warmSQL, "{cas}")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{casa}", "portuguese_prepared", "casas")
		dictionaryResult(t, p, b, wh, warmSQL, "{cas}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "portuguese_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := portugueseDefinitionSnapshot(t, p, b, c, "portuguese_prepared", "portuguese")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_prepared (language=portuguese_bogus)", "42704", `no Snowball stemmer available for language "portuguese_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "portuguese_prepared", "RUNNING")
		dictionaryResult(t, p, b, ph, parameterSQL, "{casa}", "portuguese_prepared", "casas")
		dictionaryResult(t, p, b, wh, warmSQL, "{cas}")
	})
}

func portugueseStockPrepared(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer portugueseStockCleanup(t, p, b, c)()
		const constantSQL = "SELECT ts_lexize('portuguese_stem','não') AS lexemes"
		const dynamicSQL = "SELECT ts_lexize('portuguese_stem',$1::text) AS lexemes"
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
			dictionaryResult(t, p, b, dh, dynamicSQL, "{}", "não")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_stem (stopwords)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, dh, dynamicSQL, "{nã}", "não")
		dictionaryResult(t, p, b, c, constantSQL, "{nã}")
		fresh, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{nã}")
	})
}
