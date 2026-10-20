package engine

import "testing"

func TestCheckAlterCommittedCatalogAndRetainedPins(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1))")
	sessionSQL(t, owner, "INSERT INTO t VALUES(9,1)")
	before := e.CatalogRead()
	ref := catalogRef(t, e, before, "t")
	original := before.MustRelation(ref.Address)
	pin := transactionRows(t, peer, "SELECT a FROM t", 1)
	sessionSQL(t, owner, "BEGIN")
	sessionSQL(t, owner, "SAVEPOINT shape")
	sessionSQL(t, owner, "ALTER TABLE t DROP CONSTRAINT c,DROP p")
	sessionSQL(t, owner, "INSERT INTO t VALUES(2)")
	// Observe the committed catalog without issuing a peer query behind AX.
	if e.CatalogRead().Get(ref.Address) != original {
		t.Fatal("private ALTER leaked to committed catalog")
	}
	sessionSQL(t, owner, "ROLLBACK TO shape")
	sessionSQL(t, owner, "COMMIT")
	if e.CatalogRead().Get(ref.Address) != original {
		t.Fatal("savepoint failed to restore original descriptor")
	}
	transactionRows(t, peer, "SELECT a FROM t", 1)
	sessionSQL(t, owner, "BEGIN")
	sessionSQL(t, owner, "ALTER TABLE t DROP CONSTRAINT c,DROP p")
	sessionSQL(t, owner, "INSERT INTO t VALUES(2)")
	sessionSQL(t, owner, "COMMIT")
	after := e.CatalogRead().MustRelation(ref.Address)
	if len(after.Constraints.Check) != 0 || len(after.Columns) != 1 {
		t.Fatal("Commit failed to publish CHECK removal and row shape")
	}
	if len(original.Constraints.Check) != 1 || len(original.Columns) != 2 || pin.Count != 1 {
		t.Fatal("ALTER changed old descriptor or retained rows")
	}
	transactionRows(t, peer, "SELECT a FROM t ORDER BY a", 1, 2)
}
