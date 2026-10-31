package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePorterSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY porter_prepared (TEMPLATE=snowball,Language=porter,StopWords=english)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='porter_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		constantSQL := "SELECT ts_lexize('porter_prepared'::regdictionary,'ONLY'::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE porter_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE porter_named", 0)
		regtsExec(t, p, b, c, "PREPARE porter_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE porter_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE porter_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "porter_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_prepared (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE porter_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{onli}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{onli}")
			fresh.Close()
			dictionaryResult(t, p, b, ph, parameterSQL, "{onli}", "porter_prepared", "ONLY")
			dictionaryResult(t, p, b, c, "EXECUTE porter_params('porter_prepared','ONLY')", "{onli}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{ski}", []byte("porter_prepared"), []byte("SKIES"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "porter_prepared", "RUNNING")
			dictionaryResult(t, p, b, ph, parameterSQL, nil, "porter_prepared", nil)
			dictionaryResult(t, p, b, c, parameterSQL, nil, "porter_prepared", nil)
			dictionaryResult(t, p, b, c, "EXECUTE porter_params('porter_prepared',NULL)", nil)
			porterSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ski}"}}, "porter_prepared", "SKIES"))
		}
		check()
		preserved := norwegianDefinitionSnapshot(t, p, b, c, "porter_prepared", "simple")
		porterMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_prepared (language=english,stopwords=porter_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='porter_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'porter'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{sky}", "porter_prepared", "SKIES")
		dictionaryResult(t, p, b, ch, constantSQL, "{}")
		preservedEnglish := norwegianDefinitionSnapshot(t, p, b, c, "porter_prepared", "simple")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY porter_prepared (language=porter_bogus)", "42704", `no Snowball stemmer available for language "porter_bogus" and encoding "UTF8"`)
		preservedEnglish()
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "porter_prepared", "RUNNING")
	})
}
