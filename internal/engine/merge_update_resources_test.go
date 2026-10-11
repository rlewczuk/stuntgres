package engine

import (
	"context"
	"errors"
	"testing"
)

// Initial heap replacement uses actual key changes, not the all-arm bitmap.
// Follow-chain bitmap strength has a separate public three-session witness.
func TestMergeUpdateSyntacticRowModes(t *testing.T) {
	for _, tc := range []struct {
		name, arms string
		mode       relationLockMode
	}{
		{"nonkey", "WHEN MATCHED THEN UPDATE SET v=t.v+1", noKeyUpdate},
		{"sameKey", "WHEN MATCHED THEN UPDATE SET id=t.id", noKeyUpdate},
		{"changedKey", "WHEN MATCHED THEN UPDATE SET id=t.id+1", noKeyUpdate | rowUpdate},
		{"unselectedKey", "WHEN MATCHED AND FALSE THEN UPDATE SET id=t.id WHEN MATCHED THEN UPDATE SET v=t.v+1", noKeyUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			row := e.storage[ref.Address].versions[0].row
			resource := lockResource{ObjectRef: ref, row: row}
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "SAVEPOINT before_merge")
			sessionSQL(t, s, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id "+tc.arms)
			e.locks.mu.Lock()
			entry := e.locks.entries[resource]
			var mode relationLockMode
			if entry != nil {
				mode = entry.holders[s.transactions.ticket].mode
			}
			_, journaled := s.transactions.ticket.resources[resource]
			e.locks.mu.Unlock()
			if mode != tc.mode || !journaled {
				transactionEnd(t, s, tx, false)
				t.Fatal("wrong held row mode union or missing row journal", mode, journaled, tc.mode)
			}
			sessionSQL(t, s, "ROLLBACK TO before_merge")
			e.locks.mu.Lock()
			entry = e.locks.entries[resource]
			held := entry != nil && entry.holders[s.transactions.ticket].mode != 0
			_, journaled = s.transactions.ticket.resources[resource]
			e.locks.mu.Unlock()
			if held || journaled {
				transactionEnd(t, s, tx, false)
				t.Fatal("savepoint retained action row contribution or journal")
			}
			transactionEnd(t, s, tx, false)
			lockEmpty(t, e)
		})
	}
}

func TestMergeUpdateCancellation(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20 WHEN NOT MATCHED THEN INSERT VALUES(s.id,30)"), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != 2 {
		scope.Finish(errors.New("bad count"))
		t.Fatal(result)
	}
	cancel()
	if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal("Finish lost live cancellation", err)
	}
	transactionRows(t, peer, "SELECT v FROM t", 10)
	lockEmpty(t, e)
}
