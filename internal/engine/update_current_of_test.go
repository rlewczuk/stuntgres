package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
)

func TestUpdateCurrentOfIdentity(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT 99 AS projected FROM unit_rows ORDER BY i FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 2 FROM c", [][]driver.Value{{int64(99)}, {int64(99)}})
	c := PayloadOf(s).Cursors.Entries["c"]
	source, index, root := c.CurrentSource, c.CurrentIndex, CursorRoot(c)
	ref := catalogRef(t, EngineOf(s), EngineOf(s).CatalogRead(), "unit_rows")
	identity := exec.CursorOccurrenceVersion(source, index, 0, CursorLocks(c))
	EngineOf(s).Mu.Lock()
	want := EngineOf(s).Storage.Relations[ref.Address].Versions[1].Row
	EngineOf(s).Mu.Unlock()
	if identity.Row != want {
		t.Fatal("wrong original RowID", identity.Row, want)
	}
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	if c.CurrentSource != source || c.CurrentIndex != index || CursorRoot(c) != root || c.Place != exec.CursorOn || CursorAuthority(c).Host != nil || exec.CursorOccurrenceVersion(source, index, 0, CursorLocks(c)).Row != want {
		t.Fatal("UPDATE consumed or replaced borrowed cursor authority")
	}
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 1, 3, 22)
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(99)}})
	if c.Place != exec.CursorOn {
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
	c := PayloadOf(s).Cursors.Entries["c"]
	identity := exec.CursorOccurrenceVersion(c.CurrentSource, c.CurrentIndex, 0, CursorLocks(c))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := EngineOf(s).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c RETURNING i"), 0, nil)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if err != nil || result.Count != 1 || ScopeSessionCompleted(scope) || !WriteStageReplacing(&scope.Stage) || len(scope.Stage.Rows) == 0 {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("replacement not privately staged", result, err)
	}
	staged := false
	for _, rows := range scope.Stage.Rows {
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
	for _, rows := range s.Transactions.Writes {
		for _, row := range rows {
			if row.Row == identity.Row {
				scope.Finish(context.Canceled)
				op.Done()
				t.Fatal("Execute prematurely merged replacement into transaction roots")
			}
		}
	}
	if len(s.Transactions.Deletes) != 0 {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("Execute published replacement")
	}
	cancel()
	err = scope.Finish(nil)
	op.Done()
	if !errors.Is(err, context.Canceled) || len(s.Transactions.Deletes) != 0 {
		t.Fatal("canceled Finish installed replacement", err)
	}
	for _, rows := range s.Transactions.Writes {
		for _, row := range rows {
			if row.Row == identity.Row {
				t.Fatal("canceled Finish merged replacement into transaction roots")
			}
		}
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	if PayloadOf(s).Cursors.Entries["c"] != c || c.Place != exec.CursorOn || CursorAuthority(c).Host != nil {
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
		c := PayloadOf(s).Cursors.Entries["c"]
		identity := exec.CursorOccurrenceVersion(c.CurrentSource, c.CurrentIndex, 0, CursorLocks(c))
		ref := catalogRef(t, e, e.CatalogRead(), "keyed")
		deleteCount(t, s, "UPDATE keyed SET v=v+1 WHERE CURRENT OF c", 1)
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
		ordinary := entry != nil && entry.Holders[s.Transactions.Ticket].Mode&lock.ForNoKeyUpdate != 0
		if !ordinary {
			t.Fatal("FETCH key-share skipped automatic UPDATE grant")
		}
		deleteCount(t, s, "UPDATE keyed SET id=3 WHERE CURRENT OF c", 1)
		lockState = e.Locks.Inspect()
		entry = lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
		escalated := entry != nil && entry.Holders[s.Transactions.Ticket].Mode&lock.ForUpdate != 0
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
	c := PayloadOf(worker).Cursors.Entries["c"]
	identity := exec.CursorOccurrenceVersion(c.CurrentSource, c.CurrentIndex, 0, CursorLocks(c))
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
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == worker.Transactions.Ticket && entry.Pending[0].Mode == lock.ForNoKeyUpdate
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
	old := PayloadOf(s).Cursors.Entries["c"]
	source, index := old.CurrentSource, old.CurrentIndex
	sessionSQL(t, s, "SAVEPOINT keep")
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	sessionSQL(t, s, "ROLLBACK TO keep")
	if PayloadOf(s).Cursors.Entries["c"] != old || old.CurrentSource != source || old.CurrentIndex != index {
		t.Fatal("rollback rewound borrowed identity")
	}
	deleteCount(t, s, "UPDATE unit_rows SET i=i+10 WHERE CURRENT OF c", 1)
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 2, 3, 11)
	sessionSQL(t, s, "CLOSE c")
	if old.CurrentSource != nil || old.Source != nil || CursorAuthority(old).Host != nil {
		t.Fatal("disposed cursor retains authority")
	}
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT i FROM unit_rows")
	fresh := PayloadOf(s).Cursors.Entries["c"]
	if fresh == old || fresh.CurrentSource != nil || fresh.Place != exec.CursorBefore {
		t.Fatal("new name incarnation inherited position")
	}
	sessionSQL(t, s, "ROLLBACK")
	if len(PayloadOf(s).Cursors.Entries) != 0 || fresh.CurrentSource != nil {
		t.Fatal("transaction end retains positioned authority")
	}
}
