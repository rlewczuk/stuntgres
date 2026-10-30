package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func germanStockCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	return func() {
		if !b.PG {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.german_stem (language=german,stopwords=german)"); err != nil {
			t.Error("restore canonical German stock", err)
		}
	}
}

func TestReferenceGermanSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer germanStockCleanup(t, p, b, c)()
		ddl := `CREATE TEXT SEARCH DICTIONARY "German'Prepared" (TEMPLATE=pg_catalog.snowball,Language='GeRmAn',StopWords=german)`
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, `SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='German''Prepared'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		dictionaryResult(t, p, b, c, `SELECT ts_lexize('public."German''Prepared"','HAUSER') AS lexemes`, "{haus}")
		constantSQL := "SELECT ts_lexize('german_stem','und') AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE german_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE german_named", 0)
		regtsExec(t, p, b, c, "PREPARE german_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE german_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE german_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "german_stem", "und")
		}
		old, err := c.QueryContext(p.Ctx, parameterSQL, "german_stem", "und")
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
		regtsExec(t, p, b, mutator, "ALTER TEXT SEARCH DICTIONARY german_stem (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE german_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{und}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{und}", "german_stem", "und")
			dictionaryResult(t, p, b, ph, parameterSQL, "{haus}", []byte("german_stem"), []byte("HAUSER"))
			dictionaryResult(t, p, b, c, "EXECUTE german_params('german_stem','und')", "{und}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{und}")
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
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY german_stem (stopwords=german,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY german_stem (stopwords=german)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{}", "german_stem", "und")
	})
}
