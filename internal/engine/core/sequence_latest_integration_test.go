package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

// A sequence rewrite and all three row mutations share the UPDATE-aware
// installation frontier; a replacement must not become an appended duplicate.
func TestSequenceLatestMixedUpdatePublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v BIGINT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	enginetest.SessionSQL(t, s, "CREATE SEQUENCE s")
	table := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	frontier := e.Visibility.CommitSeq()
	ref := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "ALTER SEQUENCE s RESTART 100")
	if result := enginetest.SessionSQL(t, s, "UPDATE t SET v=nextval('s') WHERE v=1"); result.Count != 1 {
		t.Fatal("UPDATE count", result.Count)
	}
	enginetest.SessionSQL(t, s, "DELETE FROM t WHERE v=2")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(7)")
	if _, err := e.Commit(enginetest.TransactionOperation(t, s), ref); err != nil {
		t.Fatal(err)
	}
	store := e.Storage.Relations[table.Address]
	if e.Visibility.CommitSeq() != frontier+1 || len(store.Versions) != 3 {
		t.Fatal("mixed publication frontier or duplicate replacement", e.Visibility.CommitSeq(), frontier, len(store.Versions))
	}
	if store.Versions[0].Born != frontier+1 || store.Versions[0].Base != 0 || store.Versions[2].Born != frontier+1 {
		t.Fatal("replacement/insert frontier", store.Versions)
	}
	if store.Ends == nil || store.Ends.Rows[store.Versions[1].Row].End != frontier+1 {
		t.Fatal("delete frontier", store.Ends)
	}
	peer := enginetest.StartSession(t, e, nil)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 7, 100)
	sequenceScalar(t, peer, "SELECT nextval('s')", 101)
}
