package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDriverBatchDependencyCascadesProjectOwnRows(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			before, frontier := e.catalog.state, e.visibility.CommitSeq()
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			for _, q := range []string{"CREATE DOMAIN d AS INT", "CREATE TABLE t(v d,x INT)", "INSERT INTO t VALUES(1,4)"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			retained, err := m.step(statement(t, "SELECT v,x FROM t"))
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Rows.Close()
			if _, err = m.step(statement(t, "DROP DOMAIN d CASCADE")); err != nil {
				t.Fatal(err)
			}
			projected, err := m.step(statement(t, "SELECT x FROM t"))
			if err != nil {
				t.Fatal(err)
			}
			defer projected.Rows.Close()
			if got := tuples(projected); !reflect.DeepEqual(got, [][]driver.Value{{int64(4)}}) {
				t.Fatal("private projected own row", got)
			}
			if commit {
				if err = m.end(nil); err != nil {
					t.Fatal(err)
				}
				transactionRows(t, s, "SELECT x FROM t", 4)
			} else {
				_, err = m.step(statement(t, "SELECT missing"))
				if err == nil {
					t.Fatal("missing suffix failure")
				}
				if end := m.end(err); end != err {
					t.Fatal(end)
				}
				if e.catalog.state != before || e.visibility.CommitSeq() != frontier {
					t.Fatal("dependency utility prefix escaped rollback")
				}
			}
			values := make([]sqltype.Value, 2)
			present, err := retained.Rows.Next(context.Background(), values)
			if err != nil || !present || !values[0].Type().IsDomain() {
				t.Fatal("retained typed domain occurrence", present, err)
			}
			base, err := values[0].BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			v, err := base.Integer()
			if err != nil || v != 1 {
				t.Fatal("retained domain datum", v, err)
			}
			x, err := values[1].Integer()
			if err != nil || x != 4 {
				t.Fatal("retained companion datum", x, err)
			}
			if present, err := retained.Rows.Next(context.Background(), values); present || err != nil {
				t.Fatal("retained domain cardinality", present, err)
			}
		})
	}
}
func TestDriverBatchSequenceCascadePrivateConsumers(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE SEQUENCE s START 5 CACHE 1")
			seq := catalogRef(t, e, e.CatalogRead(), "s")
			generator := e.storage.Sequences[seq.Address]
			before, frontier := e.catalog.state, e.visibility.CommitSeq()
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			for _, q := range []string{"CREATE TABLE t(v INT DEFAULT nextval('s'),x INT)", "INSERT INTO t(x) VALUES(1)", "DROP SEQUENCE s CASCADE", "INSERT INTO t(x) VALUES(2)"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			result, err := m.step(statement(t, "SELECT v,x FROM t ORDER BY x"))
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(5), int64(1)}, {nil, int64(2)}}) {
				t.Fatal("private dependent default consumer", got)
			}
			if generator.Last != 5 || e.storage.Sequences[seq.Address] != generator || e.catalog.state != before || e.visibility.CommitSeq() != frontier {
				t.Fatal("draw or pending sequence disposition")
			}
			if commit {
				if err = m.end(nil); err != nil {
					t.Fatal(err)
				}
				if e.storage.Sequences[seq.Address] != nil {
					t.Fatal("sequence retirement not committed")
				}
			} else {
				_, err = m.step(statement(t, "SELECT missing"))
				if err == nil {
					t.Fatal("missing suffix failure")
				}
				if end := m.end(err); end != err {
					t.Fatal(end)
				}
				if e.catalog.state != before || e.visibility.CommitSeq() != frontier || e.storage.Sequences[seq.Address] != generator {
					t.Fatal("sequence/default graph rollback")
				}
				transactionRows(t, s, "SELECT nextval('s')", 6)
			}
		})
	}
}
func TestDriverBatchOwnerCounterExhaustion(t *testing.T) {
	for _, counter := range []string{"tx", "diagnostic", "command"} {
		t.Run(counter, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			frontier := e.visibility.CommitSeq()
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			if counter == "command" {
				if _, err := m.step(statement(t, "INSERT INTO t VALUES(1)")); err != nil {
					t.Fatal(err)
				}
				s.Transactions.Command = ids.CommandID(^uint64(0))
			} else if counter == "tx" {
				s.Transactions.Next = ids.TxID(^uint64(0))
			} else {
				e.visibility.SetDiagnosticTxLocked(^uint32(0))
			}
			_, err := m.step(statement(t, "INSERT INTO t VALUES(2)"))
			var diagnostic interface{ SQLState() string }
			want := map[string]string{"tx": "stuntgres: transaction identity exhausted", "diagnostic": "stuntgres: transaction diagnostic identity exhausted", "command": "stuntgres: command identity exhausted"}[counter]
			if err == nil || err.Error() != want || errors.As(err, &diagnostic) {
				t.Fatal("counter error lost or forged SQLSTATE", err)
			}
			if end := m.end(err); end != err {
				t.Fatal("counter primary replaced", end)
			}
			if e.visibility.CommitSeq() != frontier || s.TransactionState() != TxIdle || s.Transactions.Ticket != nil || s.Transactions.RetainedMessage {
				t.Fatal("counter failure published or retained admission")
			}
			if counter == "tx" && s.Transactions.Next != ids.TxID(^uint64(0)) || counter == "diagnostic" && e.visibility.DiagnosticTx() != ^uint32(0) {
				t.Fatal("identity counter wrapped")
			}
			if got := len(e.storage.Relations[catalogRef(t, e, e.CatalogRead(), "t").Address].Versions); got != 0 {
				t.Fatal("counter failed message left prefix", got)
			}
			lockEmpty(t, e)
		})
	}
}
