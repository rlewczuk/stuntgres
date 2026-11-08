package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLateralHeldParameters(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		lateralFixture(run)
		stmt, err := c.PrepareContext(p.Ctx, lateralHeldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: lateralHeldSQL}
		for _, tc := range []struct {
			add, id any
			rows    [][]any
		}{
			{int64(100), int64(2), [][]any{{int64(2), int64(103)}}},
			{int64(-2), int64(1), [][]any{{int64(1), int64(0)}}},
			{int64(9), int64(3), [][]any{{int64(3), nil}}},
			{nil, int64(2), [][]any{{int64(2), nil}}},
			{int64(1), nil, [][]any{}},
			{int64(100), int64(2), [][]any{{int64(2), int64(103)}}},
		} {
			step := fixture.Select(lateralHeldSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, tc.rows, tc.add, tc.id)
			subqueryBoth(run, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		want := fixture.ControlsReferenceDiagnostic("22003", "integer out of range")
		subqueryQueryFailure(t, p, b, held, lateralHeldSQL, want, int64(2147483647), int64(1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(lateralHeldSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(103)}}, int64(100), int64(2)))
		run(fixture.Exec("UPDATE lat_t SET v=8 WHERE id=2", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(lateralHeldSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(108)}}, int64(100), int64(2)))
		// VALUES uses its own child frame but receives the SAME execution params.
		const valuesSQL = "SELECT t.id,f.n FROM lat_t t,LATERAL (VALUES(t.v+$1),(t.v+$1+1)) f(n) WHERE t.id=$2 ORDER BY f.n"
		valuesStmt, err := c.PrepareContext(p.Ctx, valuesSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer valuesStmt.Close()
		valuesHeld := fixture.HeldStatement{Executor: c, Statement: valuesStmt, SQL: valuesSQL}
		for _, tc := range []struct {
			add, id int64
			rows    [][]any
		}{
			{10, 1, [][]any{{int64(1), int64(12)}, {int64(1), int64(13)}}},
			{20, 2, [][]any{{int64(2), int64(28)}, {int64(2), int64(29)}}},
			{10, 1, [][]any{{int64(1), int64(12)}, {int64(1), int64(13)}}},
		} {
			step := fixture.Select(valuesSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, tc.rows, tc.add, tc.id)
			subqueryBoth(run, step)
			fixture.RunStep(t, p.Ctx, valuesHeld, b.Recorder, step)
		}
		subqueryQueryFailure(t, p, b, valuesHeld, valuesSQL, want, int64(2147483647), int64(1))
		fixture.RunStep(t, p.Ctx, valuesHeld, b.Recorder, fixture.Select(valuesSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(1), int64(13)}}, int64(10), int64(1)))
	})
}
