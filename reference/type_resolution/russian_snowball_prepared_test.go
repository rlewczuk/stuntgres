package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRussianSnowballPrepared(t *testing.T) {
	t.Run("stock-stopword-lifetime", russianStockPrepared)
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY russian_prepared (TEMPLATE=snowball,Language=russian,StopWords=russian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='russian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('russian_prepared'::regdictionary,'все'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE russian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE russian_named", 0)
		regtsExec(t, p, b, c, "PREPARE russian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE russian_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE russian_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "russian_prepared", "все")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE russian_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{все}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{все}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{все}", "russian_prepared", "все")
			dictionaryResult(t, p, b, c, "EXECUTE russian_params('russian_prepared','все')", "{все}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{машин}", []byte("russian_prepared"), []byte("машины"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{running}", "russian_prepared", "RUNNING")
		}
		check()
		preserved := portugueseDefinitionSnapshot(t, p, b, c, "russian_prepared", "russian")
		russianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_prepared (language=english,stopwords=russian_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='russian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'russian'"}})
		check()
		warmSQL := "SELECT ts_lexize('russian_prepared','машины') AS lexemes"
		warm, err := c.PrepareContext(p.Ctx, warmSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer warm.Close()
		wh := fixture.HeldStatement{Executor: c, Statement: warm, SQL: warmSQL}
		for range 6 {
			dictionaryResult(t, p, b, wh, warmSQL, "{машин}")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{машины}", "russian_prepared", "машины")
		dictionaryResult(t, p, b, wh, warmSQL, "{машин}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "russian_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := portugueseDefinitionSnapshot(t, p, b, c, "russian_prepared", "russian")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_prepared (language=russian_bogus)", "42704", `no Snowball stemmer available for language "russian_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "russian_prepared", "RUNNING")
		dictionaryResult(t, p, b, ph, parameterSQL, "{машины}", "russian_prepared", "машины")
		dictionaryResult(t, p, b, wh, warmSQL, "{машин}")
	})
}

func russianStockPrepared(t *testing.T) {
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer russianStockCleanup(t, p, b, c)()
		const constantSQL = "SELECT ts_lexize('russian_stem','все') AS lexemes"
		const dynamicSQL = "SELECT ts_lexize('russian_stem',$1::text) AS lexemes"
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
			dictionaryResult(t, p, b, dh, dynamicSQL, "{}", "все")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY russian_stem (stopwords)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, dh, dynamicSQL, "{все}", "все")
		dictionaryResult(t, p, b, c, constantSQL, "{все}")
		fresh, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{все}")
	})
}
