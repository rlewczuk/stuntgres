package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func finnishStockCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	return func() {
		if !b.PG {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.finnish_stem (language=finnish,stopwords=finnish)"); err != nil {
			t.Error("restore canonical Finnish stock", err)
		}
	}
}

func TestReferenceFinnishSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer finnishStockCleanup(t, p, b, c)()
		ddl := `CREATE TEXT SEARCH DICTIONARY "Finnish'Prepared" (TEMPLATE=pg_catalog.snowball,Language='FiNnIsH',StopWords=finnish)`
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		regtsSelect(t, p, b, c, `SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='Finnish''Prepared'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		dictionaryResult(t, p, b, c, `SELECT ts_lexize('public."Finnish''Prepared"','TALOSSA') AS lexemes`, "{talo}")
		constantSQL := "SELECT ts_lexize('finnish_stem','on') AS lexemes"
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
		regtsExec(t, p, b, c, "PREPARE finnish_named AS "+constantSQL, 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE finnish_named", 0)
		regtsExec(t, p, b, c, "PREPARE finnish_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", 0)
		defer regtsExec(t, p, b, c, "DEALLOCATE finnish_params", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE finnish_named", "{}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{}", "finnish_stem", "on")
		}
		old, err := c.QueryContext(p.Ctx, parameterSQL, "finnish_stem", "on")
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
		regtsExec(t, p, b, mutator, "ALTER TEXT SEARCH DICTIONARY finnish_stem (stopwords)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "EXECUTE finnish_named", "{}")
			dictionaryResult(t, p, b, c, constantSQL, "{on}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{on}", "finnish_stem", "on")
			dictionaryResult(t, p, b, ph, parameterSQL, "{talo}", []byte("finnish_stem"), []byte("TALOSSA"))
			dictionaryResult(t, p, b, c, "EXECUTE finnish_params('finnish_stem','on')", "{on}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{on}")
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
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_stem (stopwords=finnish,accept=true)", "22023", `unrecognized Snowball parameter: "accept"`)
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY finnish_stem (stopwords=finnish)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{}", "finnish_stem", "on")
	})
}
