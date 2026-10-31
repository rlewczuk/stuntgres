package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func lithuanianAdapterErrors(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
			rows, err := e.QueryContext(p.Ctx, q, "lithuanian_stem", make(chan int))
			if rows != nil {
				rows.Close()
				t.Fatal("argument failure exposed Rows")
			}
			fixture.DriverArgumentError(t, err)
		}
		for _, args := range [][]any{{"lithuanian_stem"}, {"lithuanian_stem", "gervė", "extra"}} {
			rows, err := stmt.QueryContext(p.Ctx, args...)
			if rows != nil {
				rows.Close()
				t.Fatal("argument count failure exposed Rows")
			}
			fixture.DriverArgumentError(t, err)
		}
		dictionaryResult(t, p, b, c, q, "{gerv}", "lithuanian_stem", "gervė")
	})
}