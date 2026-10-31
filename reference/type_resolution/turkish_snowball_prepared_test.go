package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTurkishSnowballPrepared(t *testing.T) {
	t.Run("stock-stopword-lifetime", turkishStockPrepared)
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY turkish_prepared (TEMPLATE=snowball,Language=turkish,StopWords=turkish)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='turkish_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('turkish_prepared'::regdictionary,'biz'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE turkish_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE turkish_named", 0)
		regtsExec(t, p, b, c, "PREPARE turkish_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE turkish_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE turkish_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "turkish_prepared", "biz")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY turkish_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE turkish_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{biz}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{biz}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{biz}", "turkish_prepared", "biz")
			dictionaryResult(t, p, b, c, "EXECUTE turkish_params('turkish_prepared','biz')", "{biz}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{kitap}", []byte("turkish_prepared"), []byte("kitaplar"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{cats}", "turkish_prepared", "CATS")
		}
		check()
		preserved := turkishDefinitionSnapshot(t, p, b, c, "turkish_prepared", "turkish")
		turkishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY turkish_prepared (language=english,stopwords=turkish_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='turkish_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'turkish'"}})
		check()
		warmSQL := "SELECT ts_lexize('turkish_prepared','kitaplar') AS lexemes"
		warm, err := c.PrepareContext(p.Ctx, warmSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer warm.Close()
		wh := fixture.HeldStatement{Executor: c, Statement: warm, SQL: warmSQL}
		for range 6 {
			dictionaryResult(t, p, b, wh, warmSQL, "{kitap}")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY turkish_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{kitaplar}", "turkish_prepared", "kitaplar")
		dictionaryResult(t, p, b, wh, warmSQL, "{kitap}")
		dictionaryResult(t, p, b, ph, parameterSQL, "{cat}", "turkish_prepared", "CATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := turkishDefinitionSnapshot(t, p, b, c, "turkish_prepared", "turkish")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY turkish_prepared (language=turkish_bogus)", "42704", `no Snowball stemmer available for language "turkish_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{cat}", "turkish_prepared", "CATS")
		dictionaryResult(t, p, b, ph, parameterSQL, "{kitaplar}", "turkish_prepared", "kitaplar")
		dictionaryResult(t, p, b, wh, warmSQL, "{kitap}")
	})
}

func turkishStockPrepared(t *testing.T) {
	turkishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer turkishStockCleanup(t, p, b, c)()
		const constantSQL = "SELECT ts_lexize('turkish_stem','biz') AS lexemes"
		const dynamicSQL = "SELECT ts_lexize('turkish_stem',$1::text) AS lexemes"
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
			dictionaryResult(t, p, b, dh, dynamicSQL, "{}", "biz")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY turkish_stem (stopwords)", 0)
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryResult(t, p, b, dh, dynamicSQL, "{biz}", "biz")
		dictionaryResult(t, p, b, c, constantSQL, "{biz}")
		fresh, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{biz}")
	})
}
