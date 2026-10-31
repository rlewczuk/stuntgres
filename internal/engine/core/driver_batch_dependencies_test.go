package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestDriverBatchOwnerCounterExhaustion(t *testing.T) {
	for _, counter := range []string{"tx", "diagnostic", "command"} {
		t.Run(counter, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			frontier := e.Visibility.CommitSeq()
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			if counter == "command" {
				if _, err := m.step(enginetest.Statement(t, "INSERT INTO t VALUES(1)")); err != nil {
					t.Fatal(err)
				}
				s.Transactions.Command = ids.CommandID(^uint64(0))
			} else if counter == "tx" {
				s.Transactions.Next = ids.TxID(^uint64(0))
			} else {
				e.Visibility.SetDiagnosticTxLocked(^uint32(0))
			}
			_, err := m.step(enginetest.Statement(t, "INSERT INTO t VALUES(2)"))
			var diagnostic interface{ SQLState() string }
			want := map[string]string{"tx": "stuntgres: transaction identity exhausted", "diagnostic": "stuntgres: transaction diagnostic identity exhausted", "command": "stuntgres: command identity exhausted"}[counter]
			if err == nil || err.Error() != want || errors.As(err, &diagnostic) {
				t.Fatal("counter error lost or forged SQLSTATE", err)
			}
			if end := m.end(err); end != err {
				t.Fatal("counter primary replaced", end)
			}
			if e.Visibility.CommitSeq() != frontier || s.TransactionState() != session.TxIdle || s.Transactions.Ticket != nil || s.Transactions.RetainedMessage {
				t.Fatal("counter failure published or retained admission")
			}
			if counter == "tx" && s.Transactions.Next != ids.TxID(^uint64(0)) || counter == "diagnostic" && e.Visibility.DiagnosticTx() != ^uint32(0) {
				t.Fatal("identity counter wrapped")
			}
			if got := len(e.Storage.Relations[enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address].Versions); got != 0 {
				t.Fatal("counter failed message left prefix", got)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
