package indexcmds_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

// Preserve empty-vs-nil representation and detach keys as well as entry headers,
// so later mutation of either membership or typed key values is observable.
func transactionalIndexSnapshot(root *storage.IndexRoot) []storage.IndexEntry {
	entries := slices.Clone(root.Entries)
	for i := range entries {
		entries[i].Keys = slices.Clone(entries[i].Keys)
	}
	return entries
}

func TestTransactionalIndexPrivateRoots(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "private"}[private], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE unrelated(n INT)", "INSERT INTO unrelated VALUES(90)", "CREATE INDEX unrelated_idx ON unrelated(n)"} {
				enginetest.SessionSQL(t, s, q)
			}
			_, unrelated := btreeRoot(t, e, "unrelated_idx")
			unrelatedEntries := transactionalIndexSnapshot(unrelated)
			if !private {
				enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3),(4)")
			}
			tx := enginetest.TransactionBegin(t, s)
			if private {
				enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3),(4)")
			}
			view := e.PrivateCatalogLocked(s.Transactions.Catalog)
			parent := enginetest.CatalogRef(t, e, view, "t")
			committed := e.Storage.Relations[parent.Address]
			enginetest.SessionSQL(t, s, "UPDATE t SET n=10 WHERE n=1")
			enginetest.SessionSQL(t, s, "DELETE FROM t WHERE n=2")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(5)")
			priorWrites := s.Transactions.Writes[parent.Address]
			priorCopy := append([]storage.RowVersion(nil), priorWrites...)
			enginetest.SessionSQL(t, s, "CREATE INDEX idx ON t(n)")
			journal := s.Transactions.Catalog
			view = e.PrivateCatalogLocked(journal)
			ref := enginetest.CatalogRef(t, e, view, "idx")
			idx := view.MustRelation(ref.Address)
			j := index.JournalIndexesOrZero(journal)
			root := j.UniqueRoots[ref.Address]
			candidate, ok := j.Candidates[ref.Address]
			if !ok || root == nil || candidate.Root != root || candidate.Index != idx || idx.SQLIndex.Parent != parent.Address || !candidate.MatchesIndex(idx, e.PrivateStorageLocked(journal, parent.Address)) {
				t.Fatal("invalid private candidate/identity", candidate)
			}
			wantBaseline := []string{}
			if !private {
				wantBaseline = []string{"3", "4"}
			}
			if got := btreeKeys(t, root); !reflect.DeepEqual(got, wantBaseline) {
				t.Fatal("baseline includes own writes or predecessors", got, wantBaseline)
			}
			for _, entry := range root.Entries {
				if committed == nil || entry.Position < 0 || entry.Position >= len(committed.Versions) {
					t.Fatal("invented physical position", entry)
				}
				row := committed.Versions[entry.Position]
				if row.Row != entry.Row || row.Version != entry.Version || !reflect.DeepEqual(row.Values, entry.Keys) {
					t.Fatal("wrong physical baseline identity/key", entry, row)
				}
			}
			if e.Storage.IndexRoots[ref.Address] != nil || e.Catalog.State.Get(ref.Address) != nil || e.Storage.Relations[parent.Address] != committed {
				t.Fatal("private build published early")
			}
			if !reflect.DeepEqual(priorWrites, priorCopy) {
				t.Fatal("build mutated retained prefix writes")
			}
			writes := s.Transactions.Writes[parent.Address]
			outputs := 0
			for _, row := range writes {
				if index.UniqueRowDeleted(row.Row, s.Transactions.Deletes[parent.Address]) {
					continue
				}
				out, ok := row.IndexOutputs[ref.Address]
				if !ok || out.Index != idx || !out.Member || row.Row == 0 || row.Version == 0 || !reflect.DeepEqual(out.Keys, row.Values) {
					t.Fatal("missing/wrong real own-row key output", row, out)
				}
				outputs++
			}
			wantOutputs := 2
			if private {
				wantOutputs = 4
			}
			if outputs != wantOutputs {
				t.Fatal("wrong surviving own-row output count", outputs, wantOutputs)
			}
			retained := transactionalIndexSnapshot(root)
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(6)")
			enginetest.SessionSQL(t, s, "UPDATE t SET n=30 WHERE n=3")
			enginetest.SessionSQL(t, s, "DELETE FROM t WHERE n=4")
			enginetest.SessionSQL(t, s, "SAVEPOINT keep")
			enginetest.SessionSQL(t, s, "CREATE INDEX later_idx ON t(n DESC)")
			enginetest.SessionSQL(t, s, "ROLLBACK TO keep")
			if !reflect.DeepEqual(root.Entries, retained) || !reflect.DeepEqual(priorWrites, priorCopy) {
				t.Fatal("later build/DML mutated retained baseline/prefix")
			}
			enginetest.TransactionEnd(t, s, tx, true)
			published, final := btreeRoot(t, e, "idx")
			if published.Address() != ref.Address || published.SQLIndex.Parent != parent.Address {
				t.Fatal("commit changed index/parent identity")
			}
			if got := btreeKeys(t, final); !reflect.DeepEqual(got, []string{"5", "6", "10", "30"}) {
				t.Fatal("wrong committed membership", got)
			}
			store := e.Storage.Relations[parent.Address]
			seen := map[ids.RowID]bool{}
			for _, entry := range final.Entries {
				row := store.Versions[entry.Position]
				if seen[entry.Row] || row.Row != entry.Row || row.Version != entry.Version || !reflect.DeepEqual(entry.Keys, row.Values) {
					t.Fatal("duplicated/stale/mispositioned committed key", entry, row)
				}
				seen[entry.Row] = true
			}
			if e.Storage.IndexRoots[enginetest.RoleTableRef(t, e, "unrelated_idx").Address] != unrelated || !reflect.DeepEqual(unrelated.Entries, unrelatedEntries) || !reflect.DeepEqual(root.Entries, retained) {
				t.Fatal("commit mutated retained/unrelated roots")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestTransactionalIndexSavepointRoots(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE early(n INT)", "INSERT INTO early VALUES(1),(2)", "CREATE TABLE later(n INT)"} {
		enginetest.SessionSQL(t, s, q)
	}
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "UPDATE early SET n=10 WHERE n=1")
	enginetest.SessionSQL(t, s, "DELETE FROM early WHERE n=2")
	enginetest.SessionSQL(t, s, "CREATE INDEX early_idx ON early(n)")
	prefix := s.Transactions.Catalog
	early := enginetest.RoleTableRef(t, e, "early").Address
	writes := s.Transactions.Writes[early]
	deletes := s.Transactions.Deletes[early]
	j := index.JournalIndexesOrZero(prefix)
	snapshot := prefix.Clone()
	entries := map[ids.ObjectAddress][]storage.IndexEntry{}
	for a, r := range j.UniqueRoots {
		entries[a] = transactionalIndexSnapshot(r)
	}
	enginetest.SessionSQL(t, s, "SAVEPOINT kept")
	for range 2 {
		enginetest.SessionSQL(t, s, "INSERT INTO later VALUES(3),(3)")
		enginetest.SessionSQL(t, s, "CREATE INDEX later_idx ON later(n)")
		view := e.PrivateCatalogLocked(s.Transactions.Catalog)
		later := enginetest.CatalogRef(t, e, view, "later_idx").Address
		enginetest.SessionSQL(t, s, "ROLLBACK TO kept")
		if s.Transactions.Catalog != prefix || !storage.SameVersionRoot(s.Transactions.Writes[early], writes) || s.Transactions.Deletes[early] != deletes || !reflect.DeepEqual(prefix.Clone(), snapshot) {
			t.Fatal("savepoint did not restore exact immutable prefix")
		}
		if e.PrivateCatalogLocked(prefix).Get(later) != nil || e.Storage.IndexRoots[later] != nil {
			t.Fatal("postmark descriptor/root survived")
		}
		for a, r := range j.UniqueRoots {
			if !reflect.DeepEqual(r.Entries, entries[a]) {
				t.Fatal("retained prefix root mutated")
			}
		}
	}
	enginetest.SessionSQL(t, s, "INSERT INTO later VALUES(1),(1)")
	op := enginetest.TransactionOperation(t, s)
	_, err := e.Execute(op, enginetest.Statement(t, "CREATE UNIQUE INDEX failed_idx ON later(n)"), 0, nil)
	op.Done()
	if err == nil {
		t.Fatal("duplicate build succeeded")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO kept")
	if s.Transactions.Catalog != prefix || !reflect.DeepEqual(index.JournalIndexesOrZero(prefix), j) {
		t.Fatal("failed build changed restored candidates")
	}
	enginetest.SessionSQL(t, s, "RELEASE kept")
	enginetest.TransactionEnd(t, s, tx, false)
	if s.Transactions.Catalog != nil || s.Transactions.Writes != nil || s.Transactions.Deletes != nil {
		t.Fatal("end retained private journal")
	}
	enginetest.LockEmpty(t, e)
}
