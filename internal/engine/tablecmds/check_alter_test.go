package tablecmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestCheckAlterCommittedCatalogAndRetainedPins(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1))")
	enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(9,1)")
	before := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, before, "t")
	original := before.MustRelation(ref.Address)
	pin := enginetest.TransactionRows(t, peer, "SELECT a FROM t", 1)
	enginetest.SessionSQL(t, owner, "BEGIN")
	enginetest.SessionSQL(t, owner, "SAVEPOINT shape")
	enginetest.SessionSQL(t, owner, "ALTER TABLE t DROP CONSTRAINT c,DROP p")
	enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(2)")
	// Observe the committed catalog without issuing a peer query behind AX.
	if e.CatalogRead().Get(ref.Address) != original {
		t.Fatal("private ALTER leaked to committed catalog")
	}
	enginetest.SessionSQL(t, owner, "ROLLBACK TO shape")
	enginetest.SessionSQL(t, owner, "COMMIT")
	if e.CatalogRead().Get(ref.Address) != original {
		t.Fatal("savepoint failed to restore original descriptor")
	}
	enginetest.TransactionRows(t, peer, "SELECT a FROM t", 1)
	enginetest.SessionSQL(t, owner, "BEGIN")
	enginetest.SessionSQL(t, owner, "ALTER TABLE t DROP CONSTRAINT c,DROP p")
	enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(2)")
	enginetest.SessionSQL(t, owner, "COMMIT")
	after := e.CatalogRead().MustRelation(ref.Address)
	if len(after.Constraints.Check) != 0 || len(after.Columns) != 1 {
		t.Fatal("Commit failed to publish CHECK removal and row shape")
	}
	if len(original.Constraints.Check) != 1 || len(original.Columns) != 2 || pin.Count != 1 {
		t.Fatal("ALTER changed old descriptor or retained rows")
	}
	enginetest.TransactionRows(t, peer, "SELECT a FROM t ORDER BY a", 1, 2)
}
