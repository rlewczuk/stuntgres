package engine

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

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
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(extra INT,k INT UNIQUE,v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(0,1,10)")
	ref := roleTableRef(t, e, "t")
	head := func() storage.RowVersion { return e.storage.Relations[ref.Address].Versions[0] }
	initialPrefix := e.storage.Relations[ref.Address].Versions
	initialCopy := uniqueHistoryCopy(initialPrefix)
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET k=2")
	pendingPrefix := s.Transactions.Writes[ref.Address]
	pendingCopy := uniqueHistoryCopy(pendingPrefix)
	if len(pendingPrefix) != 1 || !pendingPrefix[0].PendingKeyChange || pendingPrefix[0].KeyChangedAt != 0 {
		t.Fatal("missing private transition", pendingPrefix)
	}
	sessionSQL(t, s, "SAVEPOINT history_mark")
	sessionSQL(t, s, "UPDATE t SET k=1")
	sessionSQL(t, s, "UPDATE t SET v=20")
	if !s.Transactions.Writes[ref.Address][0].PendingKeyChange {
		t.Fatal("restoration erased pending transition")
	}
	if !reflect.DeepEqual(pendingPrefix, pendingCopy) {
		t.Fatal("replacement mutated saved private prefix")
	}
	sessionSQL(t, s, "ROLLBACK TO SAVEPOINT history_mark")
	if !reflect.DeepEqual(s.Transactions.Writes[ref.Address], pendingCopy) {
		t.Fatal("savepoint did not restore transition metadata")
	}
	transactionEnd(t, s, tx, false)
	sessionSQL(t, s, "UPDATE t SET v=30")
	if head().KeyChangedAt != 0 || head().PendingKeyChange {
		t.Fatal("rolled-back transition survived publication", head())
	}
	sessionSQL(t, s, "UPDATE t SET k=2")
	changed := head()
	if changed.KeyChangedAt == 0 || changed.KeyChangedAt != e.visibility.CommitSeq() || changed.PendingKeyChange {
		t.Fatal("transition not stamped at final publication", changed, e.visibility.CommitSeq())
	}
	changedPrefix := e.storage.Relations[ref.Address].Versions
	changedCopy := uniqueHistoryCopy(changedPrefix)
	sessionSQL(t, s, "UPDATE t SET v=40")
	if head().KeyChangedAt != changed.KeyChangedAt || head().Born <= changed.Born || head().PendingKeyChange {
		t.Fatal("payload replacement erased committed transition", head())
	}
	tx = transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET k=3")
	sessionSQL(t, s, "UPDATE t SET k=2")
	sessionSQL(t, s, "UPDATE t SET v=50")
	transactionEnd(t, s, tx, true)
	restored := head()
	if restored.KeyChangedAt != e.visibility.CommitSeq() || restored.KeyChangedAt <= changed.KeyChangedAt || restored.PendingKeyChange {
		t.Fatal("changed-and-restored transaction lost history", restored)
	}
	sessionSQL(t, s, "ALTER TABLE t DROP extra")
	sessionSQL(t, s, "ALTER TABLE t ALTER k TYPE BIGINT")
	if head().KeyChangedAt != restored.KeyChangedAt || head().PendingKeyChange {
		t.Fatal("stable projection lost row history", head())
	}
	if !reflect.DeepEqual(initialPrefix, initialCopy) || !reflect.DeepEqual(changedPrefix, changedCopy) {
		t.Fatal("publication or projection mutated retained committed prefixes")
	}
	retained := e.storage.Relations[ref.Address].Versions
	retainedCopy := uniqueHistoryCopy(retained)
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	importedRef := roleTableRef(t, e, "t")
	for _, row := range e.storage.Relations[importedRef.Address].Versions {
		if row.KeyChangedAt != 0 || row.PendingKeyChange || row.Row == restored.Row {
			t.Fatal("reminted row inherited foreign transition history", row)
		}
	}
	if !reflect.DeepEqual(retained, retainedCopy) {
		t.Fatal("import mutated source prefix")
	}
	lockEmpty(t, e)
}

func TestUniqueKeyHistoryFailedBatchAndSavepoint(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,10),(2,20)")
	ref := roleTableRef(t, e, "t")
	committed := e.storage.Relations[ref.Address].Versions
	committedCopy := uniqueHistoryCopy(committed)
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET k=3 WHERE k=1")
	pending := s.Transactions.Writes[ref.Address]
	pendingCopy := uniqueHistoryCopy(pending)
	sessionSQL(t, s, "SAVEPOINT history_mark")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, statement(t, "UPDATE t SET k=9"), 0, nil)
	op.Done()
	if result.Count != 0 || result.Rows != nil {
		t.Fatal("failed batch returned partial result", result)
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (k)=(9) already exists.", Schema: "public", Table: "t", Constraint: "u"})
	if !reflect.DeepEqual(pending, pendingCopy) || !reflect.DeepEqual(committed, committedCopy) {
		t.Fatal("failed batch mutated retained history")
	}
	sessionSQL(t, s, "ROLLBACK TO SAVEPOINT history_mark")
	if !reflect.DeepEqual(s.Transactions.Writes[ref.Address], pendingCopy) {
		t.Fatal("failed batch polluted savepoint prefix")
	}
	sessionSQL(t, s, "UPDATE t SET v=30 WHERE k=3")
	transactionEnd(t, s, tx, true)
	changed := 0
	for _, row := range e.storage.Relations[ref.Address].Versions {
		if row.PendingKeyChange {
			t.Fatal("private transition leaked into committed root", row)
		}
		if row.KeyChangedAt != 0 {
			changed++
			if row.KeyChangedAt != e.visibility.CommitSeq() || row.Row != pendingCopy[0].Row {
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
	lockEmpty(t, e)
}
