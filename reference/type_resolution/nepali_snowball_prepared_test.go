package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNepaliSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY nepali_prepared (TEMPLATE=snowball,Language=nepali,StopWords=nepali)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='nepali_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('nepali_prepared'::regdictionary,'सबै'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE nepali_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE nepali_named", 0)
		regtsExec(t, p, b, c, "PREPARE nepali_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE nepali_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE nepali_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "nepali_prepared", "सबै")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE nepali_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{सबै}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{सबै}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{सबै}", "nepali_prepared", "सबै")
			dictionaryResult(t, p, b, c, "EXECUTE nepali_params('nepali_prepared','सबै')", "{सबै}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{घर}", []byte("nepali_prepared"), []byte("घरहरू"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "nepali_prepared", "RATS")
		}
		check()
		nepaliMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_prepared (language=english,stopwords=nepali_missing)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='nepali_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'nepali'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "nepali_prepared", "RATS")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_prepared (language=nepali_bogus)", "42704", `no Snowball stemmer available for language "nepali_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{rat}", "nepali_prepared", "RATS")
	})
}
