package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/lock"
)

func TestRowLockViewRecheckModes(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				name, body, query, change string
				commit                    bool
				want                      []int64
			}{
				{"computed", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", true, []int64{21}},
				{"definition_reject", "SELECT id,v+1 AS val FROM t WHERE v=10", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", true, nil},
				{"outer_reject", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE val=11", "UPDATE t SET v=20 WHERE id=1", true, nil},
				{"on_reject", "SELECT a.id,a.v+1 AS val FROM t a JOIN t b ON a.v=b.v WHERE b.id=2", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", true, nil},
				{"delete", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1", "DELETE FROM t WHERE id=1", true, nil},
				{"rollback", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", false, []int64{11}},
				{"frozen_sort", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w ORDER BY val", "UPDATE t SET v=30 WHERE id=1", true, []int64{31, 21}},
			} {
				t.Run(tc.name+"/"+clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					e, a, b, c := rowLockEngineFixture(t)
					if tc.name == "on_reject" {
						sessionSQL(t, c, "UPDATE t SET v=10 WHERE id=2")
					}
					sessionSQL(t, c, "CREATE VIEW w AS "+tc.body)
					ar := transactionBegin(t, a)
					transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
					sessionSQL(t, a, tc.change)
					br := transactionBegin(t, b)
					query := tc.query + " " + clause
					// Describe admits metadata but is not a row-locking execution.
					if prepared {
						op, err := b.Acquire(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						_, err = e.Describe(op, statement(t, query))
						op.Done()
						if err != nil {
							t.Fatal(err)
						}
					}
					done := rowLockViewAsync(t, b, context.Background(), query)
					mode := lock.ForKeyShare
					if clause == "FOR SHARE" {
						mode = lock.ForShare
					}
					if clause == "FOR NO KEY UPDATE" {
						mode = lock.ForNoKeyUpdate
					}
					if clause == "FOR UPDATE" {
						mode = lock.ForUpdate
					}
					rowLockQueued(t, e, a.Transactions.Ticket, mode)
					sessionSQL(t, c, "INSERT INTO t VALUES(3,10)")
					transactionEnd(t, a, ar, tc.commit)
					rowLockAwait(t, done, tc.want...)
					if tc.commit && (tc.name == "definition_reject" || tc.name == "outer_reject" || tc.name == "on_reject") {
						done = rowLockViewAsync(t, c, context.Background(), "DELETE FROM t WHERE id=1")
						rowLockQueued(t, e, b.Transactions.Ticket, lock.ForUpdate)
						transactionEnd(t, b, br, false)
						deleteAwait(t, done, 1, nil)
					} else {
						transactionEnd(t, b, br, false)
					}
					lockEmpty(t, e)
				})
			}
		}
	}
}
