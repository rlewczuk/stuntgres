package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTamilSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY tamil_prepared (TEMPLATE=snowball,Language=tamil)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='tamil_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		const constantSQL = "SELECT ts_lexize('tamil_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		const tamilConstantSQL = "SELECT ts_lexize('tamil_prepared'::regdictionary,'தமிழில்'::text) AS lexemes"
		tamilConstant, err := c.PrepareContext(p.Ctx, tamilConstantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer tamilConstant.Close()
		th := fixture.HeldStatement{Executor: c, Statement: tamilConstant, SQL: tamilConstantSQL}
		regtsExec(t, p, b, c, "PREPARE tamil_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE tamil_named", 0)
		regtsExec(t, p, b, c, "PREPARE tamil_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE tamil_params", 0)
		regtsExec(t, p, b, c, "PREPARE tamil_inferred AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE tamil_inferred", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE tamil_named", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "tamil_prepared", "ONLY")
			dictionaryResult(t, p, b, th, tamilConstantSQL, "{தமிழ்}")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_prepared (stopwords=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE tamil_named", "{only}")
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
			fresh.Close()
			for _, input := range []any{"ONLY", []byte("ONLY")} {
				dictionaryResult(t, p, b, ph, parameterSQL, "{}", "tamil_prepared", input)
			}
			dictionaryResult(t, p, b, c, "EXECUTE tamil_params('tamil_prepared','ONLY')", "{}")
			dictionaryResult(t, p, b, c, "EXECUTE tamil_inferred('tamil_prepared','தமிழில்')", "{தமிழ்}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{தமிழ்}", []byte("tamil_prepared"), []byte("தமிழில்"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{running}", "tamil_prepared", "RUNNING")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "tamil_stem", "ONLY")
		}
		check()
		tamilMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_prepared (language=english,stopwords=tamil)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='tamil_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'tamil', stopwords = 'english'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "tamil_prepared", "RUNNING")
		dictionaryResult(t, p, b, ph, parameterSQL, "{தமிழில்}", "tamil_prepared", "தமிழில்")
		dictionaryResult(t, p, b, th, tamilConstantSQL, "{தமிழ்}")
		dictionaryResult(t, p, b, ch, constantSQL, "{only}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_prepared (language=tamil_bogus)", "42704", `no Snowball stemmer available for language "tamil_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "tamil_prepared", "RUNNING")
		dictionaryResult(t, p, b, th, tamilConstantSQL, "{தமிழ்}")
	})
}
