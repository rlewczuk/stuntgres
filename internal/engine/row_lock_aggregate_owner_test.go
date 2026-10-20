package engine

import "testing"

func TestRowLockAggregateStatementOwner(t *testing.T) {
	for _, joined := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "scalar", true: "joined"}[joined]+map[bool]string{false: "/select", true: "/insert"}[insert]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					e, a, b, c := rowLockEngineFixture(t)
					for _, q := range []string{"CREATE SEQUENCE s", "CREATE VIEW stored AS SELECT id FROM t FOR UPDATE", "CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(1),(2)", "CREATE TABLE destination(n BIGINT)"} {
						sessionSQL(t, c, q)
					}
					from := "stored"
					want := []int64{1, 1}
					if joined {
						from += " JOIN u USING(id)"
						want = append(want, 1)
					}
					query := "SELECT count(*) AS n FROM " + from + " GROUP BY nextval('s') ORDER BY n"
					ar := transactionBegin(t, a)
					if insert {
						result, err := rowLockViewExecute(t, e, a, "INSERT INTO destination "+query, prepared)
						if err != nil || result.Count != int64(len(want)) || result.Rows != nil {
							t.Fatal(result, err)
						}
						transactionRows(t, a, "SELECT n FROM destination ORDER BY n", want...)
					} else {
						rowLockViewRows(t, e, a, query, prepared, want...)
					}
					transactionRows(t, a, "SELECT currval('s')", int64(len(want)))
					rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1, 2)
					if joined {
						sessionSQL(t, b, "DELETE FROM u WHERE id=2")
					}
					transactionEnd(t, a, ar, false)
					transactionRows(t, c, "SELECT n FROM destination")
					sessionSQL(t, b, "DELETE FROM t WHERE id=1")
					// Grouping effects are immediate sequence state, not undone by row rollback.
					transactionRows(t, a, "SELECT currval('s')", int64(len(want)))
					lockEmpty(t, e)
				})
			}
		}
	}
}
