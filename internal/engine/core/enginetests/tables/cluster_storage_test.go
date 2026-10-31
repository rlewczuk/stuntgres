package tables_test

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func clusterStorageSeed(t *testing.T) (*core.Engine, *session.Session, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id int,k int,payload text)", "INSERT INTO t VALUES(1,2,'b'),(2,1,'a'),(3,2,'b'),(4,NULL,NULL),(5,3,'c')", "CREATE INDEX a ON t(k)", "CREATE INDEX b ON t((lower(payload)) DESC NULLS FIRST,k) INCLUDE(id)", "CREATE UNIQUE INDEX u ON t(id)", "CREATE TABLE unrelated(id int)", "INSERT INTO unrelated VALUES(9)", "CREATE INDEX unrelated_idx ON unrelated(id)"} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s, enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address
}

func clusterStorageIDs(store *storage.Relation) []int64 {
	result := make([]int64, len(store.Versions))
	for i, row := range store.Versions {
		result[i] = enginetest.Export(row.Values[0]).(int64)
	}
	return result
}

func clusterStorageIndexes(t *testing.T, e *core.Engine, parent ids.ObjectAddress) {
	t.Helper()
	store := e.Storage.Relations[parent]
	for a, relation := range e.CatalogRead().Relations() {
		if relation.SQLIndex == nil || relation.SQLIndex.Parent != parent {
			continue
		}
		root := e.Storage.IndexRoots[a]
		if root == nil || len(root.Entries) != len(store.Versions) {
			t.Fatal("missing rebuilt membership", relation.Name(), root)
		}
		for _, entry := range root.Entries {
			if entry.Position < 0 || entry.Position >= len(store.Versions) {
				t.Fatal("invalid position", entry)
			}
			row := store.Versions[entry.Position]
			if row.Row != entry.Row || row.Version != entry.Version {
				t.Fatal("index position lost stable authority", relation.Name(), entry, row)
			}
		}
	}
}

func TestClusterSimulation(t *testing.T) {
	e, s, parent := clusterStorageSeed(t)
	before := e.Storage.Relations[parent]
	generation, ends := before.Generation, before.Ends
	backing := &before.Versions[0]
	rows := append([]storage.RowVersion(nil), before.Versions...)
	for i := range rows {
		rows[i].Values = slices.Clone(rows[i].Values)
	}
	snapshots := make(map[ids.ObjectAddress]storage.IndexRoot, len(e.Storage.IndexRoots))
	roots := make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots))
	for address, root := range e.Storage.IndexRoots {
		roots[address] = root
		snapshot := *root
		snapshot.Entries = slices.Clone(root.Entries)
		for i := range snapshot.Entries {
			snapshot.Entries[i].Keys = slices.Clone(snapshot.Entries[i].Keys)
		}
		snapshot.Membership = maps.Clone(root.Membership)
		snapshot.RowEntries = maps.Clone(root.RowEntries)
		snapshots[address] = snapshot
	}
	unchanged := func() {
		t.Helper()
		after := e.Storage.Relations[parent]
		if after != before || after.Generation != generation || after.Ends != ends || &after.Versions[0] != backing || !reflect.DeepEqual(after.Versions, rows) {
			t.Fatal("simulation changed heap backing, generation, values or provenance")
		}
		for address, root := range roots {
			if e.Storage.IndexRoots[address] != root || !reflect.DeepEqual(*root, snapshots[address]) {
				t.Fatal("unchanged recipe rebuilt or mutated an index", address)
			}
		}
	}
	preference := func(name string, private bool) {
		t.Helper()
		view := e.CatalogRead()
		if private {
			e.Mu.Lock()
			view = e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
		}
		for _, descriptor := range view.Relations() {
			if descriptor.SQLIndex != nil && descriptor.SQLIndex.Parent == parent && descriptor.SQLIndex.Clustered != (descriptor.Name() == name) {
				t.Fatal("preference bits not atomic", descriptor.Name(), name)
			}
		}
	}
	for _, command := range []string{"CLUSTER t USING a", "CLUSTER t", "CLUSTER"} {
		enginetest.SessionSQL(t, s, command)
		unchanged()
		preference("a", false)
	}
	retained := e.CatalogRead().MustRelation(enginetest.CatalogRef(t, e, e.CatalogRead(), "a").Address)
	enginetest.SessionSQL(t, s, "CLUSTER t")
	if e.CatalogRead().Get(retained.Address()) != retained {
		t.Fatal("unchanged preference needlessly replaced descriptor")
	}
	for _, command := range []string{"BEGIN", "CLUSTER t USING b", "SAVEPOINT preference", "CLUSTER t USING a"} {
		enginetest.SessionSQL(t, s, command)
	}
	preference("a", true)
	preference("a", false)
	enginetest.SessionSQL(t, s, "ROLLBACK TO preference")
	preference("b", true)
	preference("a", false)
	enginetest.SessionSQL(t, s, "ROLLBACK")
	preference("a", false)
	if !retained.SQLIndex.Clustered {
		t.Fatal("mutated retained preference descriptor")
	}
	unchanged()
	enginetest.SessionSQL(t, s, "CLUSTER t USING b")
	preference("b", false)
	unchanged()
	clusterStorageIndexes(t, e, parent)
}
