package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestUpdateCurrentOfIdentity(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT 99 AS projected FROM unit_rows ORDER BY i FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 2 FROM c", [][]driver.Value{{int64(99)}, {int64(99)}})
	c := payloadOf(s).cursors.entries["c"]
	source, index, root := c.currentSource, c.currentIndex, c.root
	ref := catalogRef(t, engineOf(s), engineOf(s).CatalogRead(), "unit_rows")
	identity := cursorOccurrenceVersion(source, index, 0, c.locks)
	engineOf(s).mu.Lock()
	want := engineOf(s).storage.Relations[ref.Address].Versions[1].Row
	engineOf(s).mu.Unlock()
	if identity.Row != want {
		t.Fatal("wrong original RowID", identity.Row, want)
	}
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	if c.currentSource != source || c.currentIndex != index || c.root != root || c.position != cursorOn || c.authority.Host != nil || cursorOccurrenceVersion(source, index, 0, c.locks).Row != want {
		t.Fatal("UPDATE consumed or replaced borrowed cursor authority")
	}
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 1, 3, 22)
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(99)}})
	if c.position != cursorOn {
		t.Fatal("bounded FETCH probed EOF")
	}
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 1, 13, 22)
}

func TestUpdateCurrentOfPublication(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT i FROM unit_rows WHERE i=1 FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "INSERT INTO unit_rows VALUES(4)")
	sessionSQL(t, s, "SAVEPOINT recover")
	c := payloadOf(s).cursors.entries["c"]
	identity := cursorOccurrenceVersion(c.currentSource, c.currentIndex, 0, c.locks)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := engineOf(s).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c RETURNING i"), 0, nil)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if err != nil || result.Count != 1 || scope.sessionCompleted || !scope.stage.replacing || len(scope.stage.rows) == 0 {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("replacement not privately staged", result, err)
	}
	staged := false
	for _, rows := range scope.stage.rows {
		for _, row := range rows {
			if row.Row == identity.Row && row.Version != identity.Version && len(row.Values) == 1 && testExport(row.Values[0]) == int64(11) {
				staged = true
			}
		}
	}
	if !staged {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("no real replacement version for original identity")
	}
	for _, rows := range s.Transactions.writes {
		for _, row := range rows {
			if row.Row == identity.Row {
				scope.Finish(context.Canceled)
				op.Done()
				t.Fatal("Execute prematurely merged replacement into transaction roots")
			}
		}
	}
	if len(s.Transactions.deletes) != 0 {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("Execute published replacement")
	}
	cancel()
	err = scope.Finish(nil)
	op.Done()
	if !errors.Is(err, context.Canceled) || len(s.Transactions.deletes) != 0 {
		t.Fatal("canceled Finish installed replacement", err)
	}
	for _, rows := range s.Transactions.writes {
		for _, row := range rows {
			if row.Row == identity.Row {
				t.Fatal("canceled Finish merged replacement into transaction roots")
			}
		}
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	if payloadOf(s).cursors.entries["c"] != c || c.position != cursorOn || c.authority.Host != nil {
		t.Fatal("mutation cancellation invalidated healthy cursor")
	}
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 1, 2, 3, 4)
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 2, 3, 4, 11)
}

func TestUpdateCurrentOfArbitration(t *testing.T) {
	t.Run("key_escalation", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE keyed(id INT UNIQUE,v INT)")
		sessionSQL(t, s, "INSERT INTO keyed VALUES(1,10),(2,20)")
		sessionSQL(t, s, "BEGIN")
		sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT 99 FROM keyed WHERE id=1 FOR KEY SHARE")
		cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(99)}})
		c := payloadOf(s).cursors.entries["c"]
		identity := cursorOccurrenceVersion(c.currentSource, c.currentIndex, 0, c.locks)
		ref := catalogRef(t, e, e.CatalogRead(), "keyed")
		deleteCount(t, s, "UPDATE keyed SET v=v+1 WHERE CURRENT OF c", 1)
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
		ordinary := entry != nil && entry.Holders[s.Transactions.ticket].Mode&lock.ForNoKeyUpdate != 0
		if !ordinary {
			t.Fatal("FETCH key-share skipped automatic UPDATE grant")
		}
		deleteCount(t, s, "UPDATE keyed SET id=3 WHERE CURRENT OF c", 1)
		lockState = e.locks.Inspect()
		entry = lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
		escalated := entry != nil && entry.Holders[s.Transactions.ticket].Mode&lock.ForUpdate != 0
		if !escalated {
			t.Fatal("positioned unique-key change skipped rowUpdate escalation")
		}
		transactionRows(t, s, "SELECT id FROM keyed ORDER BY id", 2, 3)
	})
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE TABLE t(v INT)")
	sessionSQL(t, worker, "INSERT INTO t VALUES(7),(7)")
	sessionSQL(t, worker, "BEGIN")
	sessionSQL(t, worker, "DECLARE c CURSOR FOR SELECT 99 FROM t")
	cursorUnitBatch(t, worker, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(99)}})
	c := payloadOf(worker).cursors.entries["c"]
	identity := cursorOccurrenceVersion(c.currentSource, c.currentIndex, 0, c.locks)
	tx := transactionBegin(t, holder)
	deleteCount(t, holder, "UPDATE t SET v=8 WHERE v=7", 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := deleteAsync(t, worker, ctx, "UPDATE t SET v=v+1 WHERE CURRENT OF c")
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("positioned waiter did not join")
			}
		}
	}()
	lockPending(t, e, 1)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == worker.Transactions.ticket && entry.Pending[0].Mode == lock.ForNoKeyUpdate
	if !queued {
		t.Fatal("UPDATE did not request original duplicate RowID with automatic mode")
	}
	transactionEnd(t, holder, tx, false)
	deleteAwait(t, done, 1, nil)
	joined = true
	if !reflect.DeepEqual(tuples(sessionSQL(t, worker, "SELECT v FROM t ORDER BY v")), [][]driver.Value{{int64(7)}, {int64(8)}}) {
		t.Fatal("arbitration substituted equal duplicate")
	}
}

func TestUpdateCurrentOfLifetime(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT FROM unit_rows FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{}})
	old := payloadOf(s).cursors.entries["c"]
	source, index := old.currentSource, old.currentIndex
	sessionSQL(t, s, "SAVEPOINT keep")
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	sessionSQL(t, s, "ROLLBACK TO keep")
	if payloadOf(s).cursors.entries["c"] != old || old.currentSource != source || old.currentIndex != index {
		t.Fatal("rollback rewound borrowed identity")
	}
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 2, 3, 11)
	sessionSQL(t, s, "CLOSE c")
	if old.currentSource != nil || old.source != nil || old.authority.Host != nil {
		t.Fatal("disposed cursor retains authority")
	}
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT i FROM unit_rows")
	fresh := payloadOf(s).cursors.entries["c"]
	if fresh == old || fresh.currentSource != nil || fresh.position != cursorBefore {
		t.Fatal("new name incarnation inherited position")
	}
	sessionSQL(t, s, "ROLLBACK")
	if len(payloadOf(s).cursors.entries) != 0 || fresh.currentSource != nil {
		t.Fatal("transaction end retains positioned authority")
	}
}
