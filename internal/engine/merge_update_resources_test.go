package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/lock"
)

// Initial heap replacement uses actual key changes, not the all-arm bitmap.
// Follow-chain bitmap strength has a separate public three-session witness.
func TestMergeUpdateSyntacticRowModes(t *testing.T) {
	mergeUpdateRowModes(t, false)
}

func TestMergeSourceUpdateRowModes(t *testing.T) {
	mergeUpdateRowModes(t, true)
	mergeSourceFollowChainRowModes(t)
}

func mergeUpdateRowModes(t *testing.T, targetOnly bool) {
	for _, tc := range []struct {
		name, arms string
		mode       lock.Mode
	}{
		{"nonkey", "WHEN MATCHED THEN UPDATE SET v=t.v+1", lock.ForNoKeyUpdate},
		{"sameKey", "WHEN MATCHED THEN UPDATE SET id=t.id", lock.ForNoKeyUpdate},
		{"changedKey", "WHEN MATCHED THEN UPDATE SET id=t.id+1", lock.ForNoKeyUpdate | lock.ForUpdate},
		{"unselectedKey", "WHEN MATCHED AND FALSE THEN UPDATE SET id=t.id WHEN MATCHED THEN UPDATE SET v=t.v+1", lock.ForNoKeyUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			row := e.storage.Relations[ref.Address].Versions[0].Row
			resource := lock.Resource{ObjectRef: ref, Row: row}
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "SAVEPOINT before_merge")
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id " + tc.arms
			if targetOnly {
				q = "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id " + strings.ReplaceAll(tc.arms, "WHEN MATCHED", "WHEN NOT MATCHED BY SOURCE")
			}
			sessionSQL(t, s, q)
			lockState := e.locks.Inspect(s.Transactions.Ticket)
			entry := lockState.Resources[resource]
			var mode lock.Mode
			if entry != nil {
				mode = entry.Holders[s.Transactions.Ticket].Mode
			}
			_, journaled := lockState.Journals[s.Transactions.Ticket][resource]
			if mode != tc.mode || !journaled {
				transactionEnd(t, s, tx, false)
				t.Fatal("wrong held row mode union or missing row journal", mode, journaled, tc.mode)
			}
			sessionSQL(t, s, "ROLLBACK TO before_merge")
			lockState = e.locks.Inspect(s.Transactions.Ticket)
			entry = lockState.Resources[resource]
			held := entry != nil && entry.Holders[s.Transactions.Ticket].Mode != 0
			_, journaled = lockState.Journals[s.Transactions.Ticket][resource]
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
	mergeUpdateCancellation(t, false)
}

func TestMergeSourceUpdateCancellation(t *testing.T) {
	mergeUpdatePendingCancellation(t, true)
	mergeUpdateCancellation(t, true)
}

func mergeUpdateCancellation(t *testing.T, targetOnly bool) {
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
	q := "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20 WHEN NOT MATCHED THEN INSERT VALUES(s.id,30)"
	want := int64(2)
	if targetOnly {
		q = "MERGE INTO t USING (SELECT 2 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=20"
		want = 1
	}
	result, err := scope.Execute(statement(t, q), 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if result.Count != want {
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
