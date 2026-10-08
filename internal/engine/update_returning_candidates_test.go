package engine

import (
	"context"
	"testing"
)

func TestUpdateReturningFixedCandidatesAndSkippedDemand(t *testing.T) {
	for _, skip := range []bool{false, true} {
		e := New()
		holder := sessionStart(t, e, nil)
		worker := sessionStart(t, e, nil)
		peer := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(id INT,a INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE SEQUENCE s START 101 CACHE 1"} {
			sessionSQL(t, peer, q)
		}
		h := transactionBegin(t, holder)
		hold := "UPDATE t SET a=11 WHERE id=1"
		if skip {
			hold = "UPDATE t SET id=3 WHERE id=1"
		}
		sessionSQL(t, holder, hold)
		await := updateDeleteAsync(t, e, worker, context.Background(), "UPDATE t SET a=a+1 WHERE id=1 RETURNING old.a,new.a,nextval('s')")
		lockPending(t, e, 1)
		// The initially rejected row becomes matching while the worker genuinely waits.
		sessionSQL(t, peer, "UPDATE t SET id=1 WHERE id=2")
		transactionEnd(t, holder, h, true)
		r, err := await()
		if err != nil {
			t.Fatal(err)
		}
		got := tuples(r)
		r.Rows.Close()
		if skip {
			if len(got) != 0 || r.Count != 0 {
				t.Fatal(got, r.Count)
			}
			transactionRows(t, peer, "SELECT nextval('s')", 101)
		} else {
			if len(got) != 1 || got[0][0] != int64(11) || got[0][1] != int64(12) || got[0][2] != int64(101) || r.Count != 1 {
				t.Fatal(got, r.Count)
			}
			transactionRows(t, peer, "SELECT nextval('s')", 102)
		}
		transactionRows(t, peer, "SELECT a FROM t WHERE a=20", 20)
		lockEmpty(t, e)
	}
}
