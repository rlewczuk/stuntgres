package core_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func uniqueHistoryCopy(rows []storage.RowVersion) []storage.RowVersion {
	copyRows := append([]storage.RowVersion(nil), rows...)
	for i := range copyRows {
		copyRows[i].Values = append([]sqltype.Value(nil), copyRows[i].Values...)
	}
	return copyRows
}

func TestUniqueKeyHistoryPublicationAndProjection(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(extra INT,k INT UNIQUE,v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(0,1,10)")
	ref := enginetest.RoleTableRef(t, e, "t")
	head := func() storage.RowVersion { return e.Storage.Relations[ref.Address].Versions[0] }
	initialPrefix := e.Storage.Relations[ref.Address].Versions
	initialCopy := uniqueHistoryCopy(initialPrefix)
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "UPDATE t SET k=2")
	pendingPrefix := s.Transactions.Writes[ref.Address]
	pendingCopy := uniqueHistoryCopy(pendingPrefix)
	if len(pendingPrefix) != 1 || !pendingPrefix[0].PendingKeyChange || pendingPrefix[0].KeyChangedAt != 0 {
		t.Fatal("missing private transition", pendingPrefix)
	}
	enginetest.SessionSQL(t, s, "SAVEPOINT history_mark")
	enginetest.SessionSQL(t, s, "UPDATE t SET k=1")
	enginetest.SessionSQL(t, s, "UPDATE t SET v=20")
	if !s.Transactions.Writes[ref.Address][0].PendingKeyChange {
		t.Fatal("restoration erased pending transition")
	}
	if !reflect.DeepEqual(pendingPrefix, pendingCopy) {
		t.Fatal("replacement mutated saved private prefix")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO SAVEPOINT history_mark")
	if !reflect.DeepEqual(s.Transactions.Writes[ref.Address], pendingCopy) {
		t.Fatal("savepoint did not restore transition metadata")
	}
	enginetest.TransactionEnd(t, s, tx, false)
	enginetest.SessionSQL(t, s, "UPDATE t SET v=30")
	if head().KeyChangedAt != 0 || head().PendingKeyChange {
		t.Fatal("rolled-back transition survived publication", head())
	}
	enginetest.SessionSQL(t, s, "UPDATE t SET k=2")
	changed := head()
	if changed.KeyChangedAt == 0 || changed.KeyChangedAt != e.Visibility.CommitSeq() || changed.PendingKeyChange {
		t.Fatal("transition not stamped at final publication", changed, e.Visibility.CommitSeq())
	}
	changedPrefix := e.Storage.Relations[ref.Address].Versions
	changedCopy := uniqueHistoryCopy(changedPrefix)
	enginetest.SessionSQL(t, s, "UPDATE t SET v=40")
	if head().KeyChangedAt != changed.KeyChangedAt || head().Born <= changed.Born || head().PendingKeyChange {
		t.Fatal("payload replacement erased committed transition", head())
	}
	tx = enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "UPDATE t SET k=3")
	enginetest.SessionSQL(t, s, "UPDATE t SET k=2")
	enginetest.SessionSQL(t, s, "UPDATE t SET v=50")
	enginetest.TransactionEnd(t, s, tx, true)
	restored := head()
	if restored.KeyChangedAt != e.Visibility.CommitSeq() || restored.KeyChangedAt <= changed.KeyChangedAt || restored.PendingKeyChange {
		t.Fatal("changed-and-restored transaction lost history", restored)
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE t DROP extra")
	enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER k TYPE BIGINT")
	if head().KeyChangedAt != restored.KeyChangedAt || head().PendingKeyChange {
		t.Fatal("stable projection lost row history", head())
	}
	if !reflect.DeepEqual(initialPrefix, initialCopy) || !reflect.DeepEqual(changedPrefix, changedCopy) {
		t.Fatal("publication or projection mutated retained committed prefixes")
	}
	retained := e.Storage.Relations[ref.Address].Versions
	retainedCopy := uniqueHistoryCopy(retained)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	importedRef := enginetest.RoleTableRef(t, e, "t")
	for _, row := range e.Storage.Relations[importedRef.Address].Versions {
		if row.KeyChangedAt != 0 || row.PendingKeyChange || row.Row == restored.Row {
			t.Fatal("reminted row inherited foreign transition history", row)
		}
	}
	if !reflect.DeepEqual(retained, retainedCopy) {
		t.Fatal("import mutated source prefix")
	}
	enginetest.LockEmpty(t, e)
}

func TestUniqueKeyHistoryFailedBatchAndSavepoint(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,10),(2,20)")
	ref := enginetest.RoleTableRef(t, e, "t")
	committed := e.Storage.Relations[ref.Address].Versions
	committedCopy := uniqueHistoryCopy(committed)
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "UPDATE t SET k=3 WHERE k=1")
	pending := s.Transactions.Writes[ref.Address]
	pendingCopy := uniqueHistoryCopy(pending)
	enginetest.SessionSQL(t, s, "SAVEPOINT history_mark")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, enginetest.Statement(t, "UPDATE t SET k=9"), 0, nil)
	op.Done()
	if result.Count != 0 || result.Rows != nil {
		t.Fatal("failed batch returned partial result", result)
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (k)=(9) already exists.", Schema: "public", Table: "t", Constraint: "u"})
	if !reflect.DeepEqual(pending, pendingCopy) || !reflect.DeepEqual(committed, committedCopy) {
		t.Fatal("failed batch mutated retained history")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO SAVEPOINT history_mark")
	if !reflect.DeepEqual(s.Transactions.Writes[ref.Address], pendingCopy) {
		t.Fatal("failed batch polluted savepoint prefix")
	}
	enginetest.SessionSQL(t, s, "UPDATE t SET v=30 WHERE k=3")
	enginetest.TransactionEnd(t, s, tx, true)
	changed := 0
	for _, row := range e.Storage.Relations[ref.Address].Versions {
		if row.PendingKeyChange {
			t.Fatal("private transition leaked into committed root", row)
		}
		if row.KeyChangedAt != 0 {
			changed++
			if row.KeyChangedAt != e.Visibility.CommitSeq() || row.Row != pendingCopy[0].Row {
				t.Fatal("unaffected row acquired transition history", row)
			}
		}
	}
	if changed != 1 {
		t.Fatal("committed transition count", changed)
	}
	if !reflect.DeepEqual(committed, committedCopy) {
		t.Fatal("commit mutated retained pre-transaction prefix")
	}
	enginetest.LockEmpty(t, e)
}
