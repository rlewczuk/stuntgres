package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictionaryPreparedConstants(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY public.dictionary_constant (TEMPLATE=pg_catalog.simple,stopwords=english)", 0)
		constantSQL := "SELECT ts_lexize('public.dictionary_constant'::regdictionary,'YeS'::text) AS lexemes"
		constantStatement, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constantStatement.Close()
		constant := fixture.HeldStatement{Executor: c, Statement: constantStatement, SQL: constantSQL}
		parameterSQL := "SELECT ts_lexize('public.dictionary_constant'::regdictionary,$1::text) AS lexemes"
		parameterStatement, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameterStatement.Close()
		parameter := fixture.HeldStatement{Executor: c, Statement: parameterStatement, SQL: parameterSQL}
		regtsExec(t, p, b, c, "PREPARE dictionary_constant_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE dictionary_constant_named", 0)
		check := func(current any) {
			// The warmed constant belongs to its held plan, independently of
			// direct/fresh plans and calls whose token is supplied at runtime.
			dictionaryResult(t, p, b, constant, constantSQL, "{yes}")
			dictionaryResult(t, p, b, c, "EXECUTE dictionary_constant_named", "{yes}")
			dictionaryResult(t, p, b, c, constantSQL, current)
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, current)
			fresh.Close()
			dictionaryResult(t, p, b, parameter, parameterSQL, current, "YeS")
			dictionaryResult(t, p, b, c, parameterSQL, current, "YeS")
			dictionaryResult(t, p, b, parameter, parameterSQL, "{}", "The")
			dictionaryResult(t, p, b, parameter, parameterSQL, "{}", "")
			dictionaryResult(t, p, b, parameter, parameterSQL, nil, nil)
		}
		check("{yes}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_constant (accept=false)", 0)
		check(nil)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_constant (accept=true,bad=1)", "22023", `unrecognized simple dictionary parameter: "bad"`)
		check(nil)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_constant (accept)", 0)
		check("{yes}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_constant (accept=false,bad=1)", "22023", `unrecognized simple dictionary parameter: "bad"`)
		check("{yes}")
	})
}
