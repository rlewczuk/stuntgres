package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func dutchStockCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	return func() {
		if !b.PG {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.dutch_stem (language=dutch,stopwords=dutch)"); err != nil {
			t.Error("restore canonical Dutch stock", err)
		}
	}
}

func TestReferenceDutchSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer dutchStockCleanup(t, p, b, c)()
		ddl := `CREATE TEXT SEARCH DICTIONARY "Dutch'Prepared" (TEMPLATE=pg_catalog.snowball,Language='DuTcH',StopWords=dutch)`
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, `SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='Dutch''Prepared'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		dictionaryResult(t, p, b, c, `SELECT ts_lexize('public."Dutch''Prepared"','HONDEN') AS lexemes`, "{hond}")
		constantSQL := "SELECT ts_lexize('dutch_stem','de') AS lexemes"
		parameterSQL := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE dutch_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE dutch_named", 0)
		regtsExec(t, p, b, c, "PREPARE dutch_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE dutch_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE dutch_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "dutch_stem", "de")
		}
		old, err := c.QueryContext(p.Ctx, parameterSQL, "dutch_stem", "de")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal(types, err)
		}
		// pq streams forbid concurrent SQL on the retained cursor's connection.
		mutator, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer mutator.Close()
		regtsExec(t, p, b, mutator, "ALTER TEXT SEARCH DICTIONARY dutch_stem (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE dutch_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{de}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{de}", "dutch_stem", "de")
			dictionaryResult(t, p, b, ph, parameterSQL, "{hond}", []byte("dutch_stem"), []byte("HONDEN"))
			dictionaryResult(t, p, b, c, "EXECUTE dutch_params('dutch_stem','de')", "{de}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{de}")
			fresh.Close()
		}
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var value string
		if err := old.Scan(&value); err != nil || value != "{}" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
		old.Close()
		check()
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dutch_stem (stopwords=dutch,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dutch_stem (stopwords=dutch)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{}", "dutch_stem", "de")
	})
}
