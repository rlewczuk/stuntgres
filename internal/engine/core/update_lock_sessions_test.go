package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestUpdateCoreRowDeadlinesAndExplicitAbort(t *testing.T) {
	for _, setting := range []struct{ q, state, message string }{{"SET lock_timeout='2ms'", "55P03", "canceling statement due to lock timeout"}, {"SET statement_timeout='2ms'", "57014", "canceling statement due to statement timeout"}} {
		t.Run(setting.state, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ta := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
			tb := enginetest.TransactionBegin(t, b)
			enginetest.SessionSQL(t, b, setting.q)
			done := waitsCommandAsync(e, b, context.Background(), "UPDATE t SET v=3", false)
			select {
			case err := <-done:
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != setting.state || diagnostic.Message != setting.message || diagnostic.Position != "" {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("SQL deadline did not stop row wait")
			}
			if b.TransactionState() != session.TxAborted {
				t.Fatal("error did not abort explicit block")
			}
			enginetest.TransactionEnd(t, b, tb, false)
			enginetest.TransactionEnd(t, a, ta, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
