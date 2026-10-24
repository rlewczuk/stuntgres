package engine

import (
	"reflect"
	"slices"
	"testing"

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
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE unrelated(n INT)", "INSERT INTO unrelated VALUES(90)", "CREATE INDEX unrelated_idx ON unrelated(n)"} {
				sessionSQL(t, s, q)
			}
			_, unrelated := btreeRoot(t, e, "unrelated_idx")
			unrelatedEntries := transactionalIndexSnapshot(unrelated)
			if !private {
				sessionSQL(t, s, "CREATE TABLE t(n INT)")
				sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3),(4)")
			}
			tx := transactionBegin(t, s)
			if private {
				sessionSQL(t, s, "CREATE TABLE t(n INT)")
				sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3),(4)")
			}
			view := e.privateCatalogLocked(s.Transactions.Catalog)
			parent := catalogRef(t, e, view, "t")
			committed := e.storage.Relations[parent.Address]
			sessionSQL(t, s, "UPDATE t SET n=10 WHERE n=1")
			sessionSQL(t, s, "DELETE FROM t WHERE n=2")
			sessionSQL(t, s, "INSERT INTO t VALUES(5)")
			priorWrites := s.Transactions.Writes[parent.Address]
			priorCopy := append([]storage.RowVersion(nil), priorWrites...)
			sessionSQL(t, s, "CREATE INDEX idx ON t(n)")
			journal := s.Transactions.Catalog
			view = e.privateCatalogLocked(journal)
			ref := catalogRef(t, e, view, "idx")
			idx := view.MustRelation(ref.Address)
			j := index.JournalIndexesOrZero(journal)
			root := j.UniqueRoots[ref.Address]
			candidate, ok := j.Candidates[ref.Address]
			if !ok || root == nil || candidate.Root != root || candidate.Index != idx || idx.SQLIndex.Parent != parent.Address || !candidate.MatchesIndex(idx, e.privateStorageLocked(journal, parent.Address)) {
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
			if e.storage.IndexRoots[ref.Address] != nil || e.catalog.state.Get(ref.Address) != nil || e.storage.Relations[parent.Address] != committed {
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
			sessionSQL(t, s, "INSERT INTO t VALUES(6)")
			sessionSQL(t, s, "UPDATE t SET n=30 WHERE n=3")
			sessionSQL(t, s, "DELETE FROM t WHERE n=4")
			sessionSQL(t, s, "SAVEPOINT keep")
			sessionSQL(t, s, "CREATE INDEX later_idx ON t(n DESC)")
			sessionSQL(t, s, "ROLLBACK TO keep")
			if !reflect.DeepEqual(root.Entries, retained) || !reflect.DeepEqual(priorWrites, priorCopy) {
				t.Fatal("later build/DML mutated retained baseline/prefix")
			}
			transactionEnd(t, s, tx, true)
			published, final := btreeRoot(t, e, "idx")
			if published.Address() != ref.Address || published.SQLIndex.Parent != parent.Address {
				t.Fatal("commit changed index/parent identity")
			}
			if got := btreeKeys(t, final); !reflect.DeepEqual(got, []string{"5", "6", "10", "30"}) {
				t.Fatal("wrong committed membership", got)
			}
			store := e.storage.Relations[parent.Address]
			seen := map[ids.RowID]bool{}
			for _, entry := range final.Entries {
				row := store.Versions[entry.Position]
				if seen[entry.Row] || row.Row != entry.Row || row.Version != entry.Version || !reflect.DeepEqual(entry.Keys, row.Values) {
					t.Fatal("duplicated/stale/mispositioned committed key", entry, row)
				}
				seen[entry.Row] = true
			}
			if e.storage.IndexRoots[roleTableRef(t, e, "unrelated_idx").Address] != unrelated || !reflect.DeepEqual(unrelated.Entries, unrelatedEntries) || !reflect.DeepEqual(root.Entries, retained) {
				t.Fatal("commit mutated retained/unrelated roots")
			}
			lockEmpty(t, e)
		})
	}
}

func TestTransactionalIndexSavepointRoots(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE early(n INT)", "INSERT INTO early VALUES(1),(2)", "CREATE TABLE later(n INT)"} {
		sessionSQL(t, s, q)
	}
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE early SET n=10 WHERE n=1")
	sessionSQL(t, s, "DELETE FROM early WHERE n=2")
	sessionSQL(t, s, "CREATE INDEX early_idx ON early(n)")
	prefix := s.Transactions.Catalog
	early := roleTableRef(t, e, "early").Address
	writes := s.Transactions.Writes[early]
	deletes := s.Transactions.Deletes[early]
	j := index.JournalIndexesOrZero(prefix)
	snapshot := prefix.Clone()
	entries := map[ids.ObjectAddress][]storage.IndexEntry{}
	for a, r := range j.UniqueRoots {
		entries[a] = transactionalIndexSnapshot(r)
	}
	sessionSQL(t, s, "SAVEPOINT kept")
	for range 2 {
		sessionSQL(t, s, "INSERT INTO later VALUES(3),(3)")
		sessionSQL(t, s, "CREATE INDEX later_idx ON later(n)")
		view := e.privateCatalogLocked(s.Transactions.Catalog)
		later := catalogRef(t, e, view, "later_idx").Address
		sessionSQL(t, s, "ROLLBACK TO kept")
		if s.Transactions.Catalog != prefix || !storage.SameVersionRoot(s.Transactions.Writes[early], writes) || s.Transactions.Deletes[early] != deletes || !reflect.DeepEqual(prefix.Clone(), snapshot) {
			t.Fatal("savepoint did not restore exact immutable prefix")
		}
		if e.privateCatalogLocked(prefix).Get(later) != nil || e.storage.IndexRoots[later] != nil {
			t.Fatal("postmark descriptor/root survived")
		}
		for a, r := range j.UniqueRoots {
			if !reflect.DeepEqual(r.Entries, entries[a]) {
				t.Fatal("retained prefix root mutated")
			}
		}
	}
	sessionSQL(t, s, "INSERT INTO later VALUES(1),(1)")
	op := transactionOperation(t, s)
	_, err := e.Execute(op, statement(t, "CREATE UNIQUE INDEX failed_idx ON later(n)"), 0, nil)
	op.Done()
	if err == nil {
		t.Fatal("duplicate build succeeded")
	}
	sessionSQL(t, s, "ROLLBACK TO kept")
	if s.Transactions.Catalog != prefix || !reflect.DeepEqual(index.JournalIndexesOrZero(prefix), j) {
		t.Fatal("failed build changed restored candidates")
	}
	sessionSQL(t, s, "RELEASE kept")
	transactionEnd(t, s, tx, false)
	if s.Transactions.Catalog != nil || s.Transactions.Writes != nil || s.Transactions.Deletes != nil {
		t.Fatal("end retained private journal")
	}
	lockEmpty(t, e)
}
