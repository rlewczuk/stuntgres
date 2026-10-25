package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestUpdateReturningCancellation(t *testing.T) {
	for _, wins := range []bool{false, true} {
		t.Run(map[bool]string{false: "publication_veto", true: "postpublication"}[wins], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			op := enginetest.TransactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, "UPDATE t SET v=v+1 RETURNING old.v,new.v"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			defer result.Rows.Close()
			base, cancel := context.WithCancel(op.Context())
			defer cancel()
			if wins {
				op.Ctx = &deleteCancelAfterCheck{Context: base, cancel: cancel, at: 2}
			} else {
				op.Ctx = &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}
			}
			err = scope.Finish(nil)
			if wins {
				if err != nil {
					t.Fatal(err)
				}
				enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 2, 3)
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				result.Rows.Close()
				enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
