package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func tamilAdapterErrors(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
			rows, err := e.QueryContext(p.Ctx, q, "tamil_stem", make(chan int))
			if rows != nil {
				rows.Close()
				t.Fatal("argument failure exposed Rows")
			}
			fixture.DriverArgumentError(t, err)
		}
		for _, args := range [][]any{{"tamil_stem"}, {"tamil_stem", "தமிழில்", "extra"}} {
			rows, err := stmt.QueryContext(p.Ctx, args...)
			if rows != nil {
				rows.Close()
				t.Fatal("argument count failure exposed Rows")
			}
			fixture.DriverArgumentError(t, err)
		}
		dictionaryResult(t, p, b, c, q, "{தமிழ்}", "tamil_stem", "தமிழில்")
	})
}
func TestReferenceTamilSnowballAdapter(t *testing.T) {
	tamilAdapterErrors(t)
}
