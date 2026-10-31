package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRomanianSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY romanian_prepared (TEMPLATE=snowball,Language=romanian)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='romanian_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		const constantSQL = "SELECT ts_lexize('romanian_prepared'::regdictionary,'THE'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE romanian_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE romanian_named", 0)
		regtsExec(t, p, b, c, "PREPARE romanian_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE romanian_params", 0)
		regtsExec(t, p, b, c, "PREPARE romanian_inferred AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE romanian_inferred", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{the}")
			dictionaryResult(t, p, b, c, "EXECUTE romanian_named", "{the}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{the}", "romanian_prepared", "THE")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_prepared (stopwords=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{the}")
			dictionaryResult(t, p, b, c, "EXECUTE romanian_named", "{the}")
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
			fresh.Close()
			for _, input := range []any{"THE", []byte("THE"), "ONLY", []byte("ONLY")} {
				dictionaryResult(t, p, b, ph, parameterSQL, "{}", "romanian_prepared", input)
			}
			dictionaryResult(t, p, b, c, "EXECUTE romanian_params('romanian_prepared','ONLY')", "{}")
			dictionaryResult(t, p, b, c, "EXECUTE romanian_inferred('romanian_prepared','abruptă')", "{abrupt}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{abrupt}", []byte("romanian_prepared"), []byte("abruptă"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{ocol}", "romanian_prepared", "OCOLUL")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "romanian_stem", "ONLY")
		}
		check()
		romanianMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_prepared (language=english,stopwords=romanian)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'romanian', stopwords = 'english'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_prepared (stopwords)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{the}", "romanian_prepared", "THE")
		dictionaryResult(t, p, b, c, constantSQL, "{the}")
		dictionaryResult(t, p, b, ch, constantSQL, "{the}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "romanian_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{the}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY romanian_prepared (language=romanian_bogus)", "42704", `no Snowball stemmer available for language "romanian_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "romanian_prepared", "RUNNING")
	})
}
