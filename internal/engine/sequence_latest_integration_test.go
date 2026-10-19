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
	frontier := e.commitSeq
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
	store := e.storage.relations[table.Address]
	if e.commitSeq != frontier+1 || len(store.versions) != 3 {
		t.Fatal("mixed publication frontier or duplicate replacement", e.commitSeq, frontier, len(store.versions))
	}
	if store.versions[0].born != frontier+1 || store.versions[0].base != 0 || store.versions[2].born != frontier+1 {
		t.Fatal("replacement/insert frontier", store.versions)
	}
	if store.ends == nil || store.ends.rows[store.versions[1].row].end != frontier+1 {
		t.Fatal("delete frontier", store.ends)
	}
	peer := sessionStart(t, e, nil)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 7, 100)
	sequenceScalar(t, peer, "SELECT nextval('s')", 101)
}
