package stuntgres_test

import "testing"

func TestRowLockAggregateStatementOwner(t *testing.T) {
	for _, joined := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "scalar", true: "joined"}[joined]+map[bool]string{false: "/select", true: "/insert"}[insert]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					_, a, b, ctx := rowLockPublicViewFixture(t)
					for _, q := range []string{"CREATE SEQUENCE s", "CREATE VIEW stored AS SELECT id FROM t WHERE id<3 FOR UPDATE", "CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(1),(2)", "CREATE TABLE destination(n BIGINT)"} {
						if _, err := a.ExecContext(ctx, q); err != nil {
							t.Fatal(err)
						}
					}
					from := "stored"
					want := []int64{1, 1}
					if joined {
						from += " JOIN u USING(id)"
						want = append(want, 1)
					}
					query := "SELECT count(*) AS n FROM " + from + " GROUP BY nextval('s') ORDER BY n"
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					if insert {
						transactionAffected(t, ctx, holder, "INSERT INTO destination "+query, prepared, int64(len(want)))
						rowLockPublicSelect(t, ctx, holder, "SELECT n FROM destination ORDER BY n", false, "n", "INT8", want...)
					} else {
						rowLockPublicSelect(t, ctx, holder, query, prepared, "n", "INT8", want...)
					}
					rowLockPublicSelect(t, ctx, holder, "SELECT currval('s') AS n", false, "n", "INT8", int64(len(want)))
					rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
					transactionAffected(t, ctx, b, "UPDATE t SET v=v WHERE id=3", false, 1)
					if joined {
						transactionAffected(t, ctx, b, "DELETE FROM u WHERE id=2", false, 1)
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					rowLockPublicSelect(t, ctx, b, "SELECT n FROM destination", false, "n", "INT8")
					transactionAffected(t, ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
					rowLockPublicSelect(t, ctx, a, "SELECT currval('s') AS n", false, "n", "INT8", int64(len(want)))
				})
			}
		}
	}
}
