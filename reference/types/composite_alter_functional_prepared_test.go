package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterFunctionalPrepared(t *testing.T) {
	for _, firstType := range []string{"integer", "text"} {
		t.Run(firstType, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TYPE pair AS (id "+firstType+",note text)", `CREATE TABLE holder(k integer,v pair)`)
				first := "7"
				if firstType == "text" {
					first = "not-the-note"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,$1),(2,NULL)`, 2, "("+first+",original)"))
				// No AS on note(v): its original output label is part of the contract.
				q := `SELECT note(v),(v).note AS dot FROM holder ORDER BY k`
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				observe := func(value any) {
					for range 2 {
						s := fixture.Select(q, []string{"note", "dot"}, []string{"TEXT", "TEXT"}, [][]any{{value, value}, {nil, nil}})
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
					}
				}
				observe("original")
				for _, ddl := range []string{`ALTER TYPE pair ADD ATTRIBUTE extra text`, `ALTER TYPE pair RENAME ATTRIBUTE note TO label`} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
					observe("original")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE label`, 0))
				observe(nil)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TYPE pair ADD ATTRIBUTE note text`, 0))
				// A current same-spelled field must not resurrect the held selection.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET v=$1 WHERE k=1`, 1, "("+first+",extra,new-note)"))
				observe(nil)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT note(v),(v).note AS dot FROM holder ORDER BY k`, []string{"note", "dot"}, []string{"TEXT", "TEXT"}, [][]any{{"new-note", "new-note"}, {nil, nil}}))
				for _, ddl := range []string{`ALTER TYPE pair DROP ATTRIBUTE note`, `ALTER TYPE pair DROP ATTRIBUTE extra`, `ALTER TYPE pair DROP ATTRIBUTE id`} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
					observe(nil)
				}
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"()"}, {nil}}, true)
			})
		})
	}
}
