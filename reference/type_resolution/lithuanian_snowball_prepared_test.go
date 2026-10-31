package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLithuanianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY lithuanian_prepared (TEMPLATE=snowball,Language=lithuanian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='lithuanian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		const constantSQL = "SELECT ts_lexize('lithuanian_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE lithuanian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE lithuanian_named", 0)
		regtsExec(t, p, b, c, "PREPARE lithuanian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE lithuanian_params", 0)
		regtsExec(t, p, b, c, "PREPARE lithuanian_inferred AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE lithuanian_inferred", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{onl}")
			dictionaryResult(t, p, b, c, "EXECUTE lithuanian_named", "{onl}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{onl}", "lithuanian_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY lithuanian_prepared (stopwords=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{onl}")
			dictionaryResult(t, p, b, c, "EXECUTE lithuanian_named", "{onl}")
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
			fresh.Close()
			for _, input := range []any{"ONLY", []byte("ONLY")} {
				dictionaryResult(t, p, b, ph, parameterSQL, "{}", "lithuanian_prepared", input)
			}
			dictionaryResult(t, p, b, c, "EXECUTE lithuanian_params('lithuanian_prepared','ONLY')", "{}")
			dictionaryResult(t, p, b, c, "EXECUTE lithuanian_inferred('lithuanian_prepared','gervė')", "{gerv}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{gerv}", []byte("lithuanian_prepared"), []byte("gervė"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{vyr}", "lithuanian_prepared", "VYRAS")
			dictionaryResult(t, p, b, ph, parameterSQL, "{onl}", "lithuanian_stem", "ONLY")
		}
		check()
		lithuanianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY lithuanian_prepared (language=english,stopwords=lithuanian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='lithuanian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'lithuanian', stopwords = 'english'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY lithuanian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "lithuanian_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{onl}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY lithuanian_prepared (language=lithuanian_bogus)", "42704", `no Snowball stemmer available for language "lithuanian_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "lithuanian_prepared", "RUNNING")
	})
}
