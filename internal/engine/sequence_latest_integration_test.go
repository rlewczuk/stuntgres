package engine

import "testing"

// A sequence rewrite and all three row mutations share the UPDATE-aware
// installation frontier; a replacement must not become an appended duplicate.
func TestSequenceLatestMixedUpdatePublication(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v BIGINT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	sessionSQL(t, s, "CREATE SEQUENCE s")
	table := catalogRef(t, e, e.CatalogRead(), "t")
	frontier := e.Visibility.CommitSeq()
	ref := transactionBegin(t, s)
	sessionSQL(t, s, "ALTER SEQUENCE s RESTART 100")
	if result := sessionSQL(t, s, "UPDATE t SET v=nextval('s') WHERE v=1"); result.Count != 1 {
		t.Fatal("UPDATE count", result.Count)
	}
	sessionSQL(t, s, "DELETE FROM t WHERE v=2")
	sessionSQL(t, s, "INSERT INTO t VALUES(7)")
	if _, err := e.Commit(transactionOperation(t, s), ref); err != nil {
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
	peer := sessionStart(t, e, nil)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 7, 100)
	sequenceScalar(t, peer, "SELECT nextval('s')", 101)
}
