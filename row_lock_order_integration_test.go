package stuntgres_test

import "testing"

// This regression specifies the newly integrated ordering contract through the
// existing public APIs, before the locking-stage cache propagation is remedied.
func TestRowLockOrderingIntegrationFailingFirst(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range rowLockPublicClauses {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, _, ctx := rowLockPublicViewFixture(t)
				for _, test := range []struct {
					query string
					want  []int64
				}{
					{"SELECT v+1 AS val FROM t WHERE id<3 ORDER BY val DESC NULLS FIRST,id ASC " + clause, []int64{21, 11}},
					{"SELECT val+1 AS shifted FROM w ORDER BY shifted DESC NULLS LAST,id ASC " + clause, []int64{22, 12}},
				} {
					transactionSelect(t, ctx, a, test.query, prepared, test.want...)
				}
				transactionAffected(t, ctx, a, "CREATE VIEW ordered_stored AS SELECT id,v FROM t WHERE id<3 ORDER BY v+1 DESC,id ASC FOR UPDATE", false, 0)
				transactionSelect(t, ctx, a, "SELECT v+1 AS val FROM ordered_stored ORDER BY val DESC NULLS FIRST,id ASC", prepared, 21, 11)
			})
		}
	}
}
