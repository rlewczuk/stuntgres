package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func plCursorBoundOrdering(t *testing.T) {
	t.Helper()
	t.Run("bound_actual_declaration_order_once", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE pc_bound_seq", 0))
			plCursorFunction(t, p, b, c, "pc_bound_order", "", "bigint", "DECLARE c CURSOR(a bigint,b bigint) FOR SELECT a*100+b; x bigint; BEGIN OPEN c(b=>nextval('pc_bound_seq'),a=>nextval('pc_bound_seq')); FETCH c INTO x; CLOSE c; RETURN x; END")
			q := "SELECT pc_bound_order() AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			// Pinned 18.6 evaluates the SELECT-INTO argument projection in
			// declaration order after named-argument mapping, not written order.
			sqlCursorStep(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(102)}}))
			sqlCursorStep(t, p, b, c, fixture.Prepared(fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(304)}})))
			for _, want := range []int64{506, 708} {
				sqlCursorStep(t, p, b, held, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{want}}))
			}
			plCursorValue(t, p, b, c, "SELECT currval('pc_bound_seq') AS v", "INT8", int64(8))
		})
	})
}
