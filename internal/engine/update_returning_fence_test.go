package engine

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateReturningCancellation(t *testing.T) {
	for _, wins := range []bool{false, true} {
		t.Run(map[bool]string{false: "publication_veto", true: "postpublication"}[wins], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			op := transactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(statement(t, "UPDATE t SET v=v+1 RETURNING old.v,new.v"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			defer result.Rows.Close()
			base, cancel := context.WithCancel(op.Context())
			defer cancel()
			if wins {
				op.ctx = &deleteCancelAfterCheck{Context: base, cancel: cancel, at: 2}
			} else {
				op.ctx = &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}
			}
			err = scope.Finish(nil)
			if wins {
				if err != nil {
					t.Fatal(err)
				}
				transactionRows(t, s, "SELECT v FROM t ORDER BY v", 2, 3)
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				result.Rows.Close()
				transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
			}
			lockEmpty(t, e)
		})
	}
}
