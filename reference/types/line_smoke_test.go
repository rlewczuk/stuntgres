package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// A held insert remains usable after a geometric semantic failure, and failed
// publication leaves the typed nullable relation unchanged.
func TestReferenceLineFeatureSmoke(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE line_smoke(id integer NOT NULL,l line)", 0))
		run(fixture.Exec("INSERT INTO line_smoke VALUES(1,'{2,4,6}')", 1))
		q := "INSERT INTO line_smoke VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "[(1,2),(3,4)]"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, []byte(nil)))
		want := [][]any{{int64(1), "{2,4,6}"}, {int64(2), "{1,-1,1}"}, {int64(3), nil}}
		run(lineSelect(b, "SELECT id,l FROM line_smoke ORDER BY id", []string{"id", "l"}, []string{"INT4", "LINE"}, want))
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid line specification: must be two distinct points")
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, 4, "(1,2),(1,2)")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(lineSelect(b, "SELECT id,l FROM line_smoke ORDER BY id", []string{"id", "l"}, []string{"INT4", "LINE"}, want))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 4, "{2,4,6}"))
		run(fixture.Select("SELECT line '{1,2,3}'=line '{2,4,6}' AS eq,line '{1,2,3}'=line '{2,4,7}' AS ne", []string{"eq", "ne"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
	})
}
