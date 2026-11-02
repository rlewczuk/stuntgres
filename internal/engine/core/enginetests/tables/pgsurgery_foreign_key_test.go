package tables_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeForeignKeyCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(k INT PRIMARY KEY)", "CREATE TABLE c(k INT REFERENCES p)", "INSERT INTO p VALUES(1)"} {
		enginetest.SessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	parentRef := enginetest.CatalogRef(t, e, view, "p")
	childRef := enginetest.CatalogRef(t, e, view, "c")
	parent, child := view.MustRelation(parentRef.Address), view.MustRelation(childRef.Address)
	f := child.Constraints.ForeignKeys[0]
	idx := view.MustRelation(f.Index)
	comparisons, err := table.BindForeignKeyComparison(view, child, parent, idx, f)
	if err != nil {
		t.Fatal(err)
	}
	scope := foreignActionUnitScope(t, e, s, "SELECT 1")
	h := scope.WriteHost()
	before, err := h.ForeignKeyRows(parentRef, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	a := before.Committed[0]
	enginetest.SessionSQL(t, peer, "UPDATE p SET k=2")
	e.Mu.Lock()
	headers, err := storage.RepairHeapHeaders(before.Storage.Headers, []storage.RowVersion{a})
	if err == nil {
		before.Storage.Headers = headers
		before.Storage.Generation++
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := h.ForeignKeyRows(parentRef, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Headers != headers || before.Headers != nil || before.Committed[0].ForcedFrozen || &before.Committed[0].Values[0] != &a.Values[0] || fresh.Storage != before.Storage || fresh.Root == before.Root {
		t.Fatal("capture lost immutable root/payload or adopted a different heap incarnation")
	}
	versions := make(map[uint64]storage.RowVersion)
	if err := fresh.Each(func(row storage.RowVersion) (bool, error) {
		if _, exists := versions[uint64(row.Version)]; exists {
			t.Fatal("composed RI capture duplicated a physical version", row.Version)
		}
		versions[uint64(row.Version)] = row
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || !versions[uint64(a.Version)].ForcedFrozen || versions[uint64(a.Version)].Row != fresh.Committed[0].Row {
		t.Fatal("fresh repaired sibling did not survive independently", versions)
	}
	key := make([]sqltype.Value, len(comparisons))
	if err := table.PrepareForeignKeyChild(context.Background(), comparisons, a.Values, key, sqltype.InputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	matching, _, err := table.FindForeignKeyParent(context.Background(), idx, comparisons, fresh, key, sqltype.InputEnvironment{})
	if err != nil || matching.Row != 0 || fresh.Root != e.Storage.IndexRoots[f.Index] || fresh.Root.IsMember(a.Row, a.Version) {
		t.Fatal("unsafe heap repair recreated supporting-index membership", matching, err)
	}
	if err := h.RefreshForeignKeyRows(parentRef, &before); err != nil {
		t.Fatal(err)
	}
	if before.Headers != fresh.Headers || before.Root != fresh.Root || !storage.SameVersionRoot(before.Committed, fresh.Committed) {
		t.Fatal("post-grant refresh did not capture a coherent canonical heap/index pair")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.Op.Done()
}

func TestPgsurgeryFreezeForeignKeyComposition(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(k INT PRIMARY KEY)", "CREATE TABLE c(k INT REFERENCES p ON DELETE CASCADE,note INT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1,0)"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
	store := e.Storage.Relations[ref.Address]
	a := store.Versions[0]
	enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.SessionSQL(t, s, "SELECT * FROM c")
	enginetest.SessionSQL(t, peer, "UPDATE c SET note=1")
	b := store.Versions[0]
	if a.Row != b.Row || a.Version == b.Version {
		t.Fatal("fixture lacks genuine same-RowID committed successor")
	}
	e.Mu.Lock()
	headers, err := storage.RepairHeapHeaders(store.Headers, []storage.RowVersion{a})
	if err == nil {
		store.Headers = headers
		store.Generation++
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope := foreignActionUnitScope(t, e, s, "DELETE FROM p WHERE k=1")
	h := scope.WriteHost()
	current, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := h.RetainedForeignKeyRows(ref, current)
	if err != nil || retained.Headers != current.Headers || retained.Headers != headers || !storage.SameVersionRoot(retained.Committed, scope.Owner.Roots[ref.Address].Versions) {
		t.Fatal("stable RI capture lost the canonical header/root pairing", err)
	}
	err = dml.SettleForeignActions(context.Background(), h)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "40001" {
		t.Fatal("fresh independent sibling escaped actual latest-row crosscheck", err)
	}
	if !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: ref, Row: b.Row}, lock.ForUpdate) {
		t.Fatal("snapshot crosscheck bypassed the canonical row grant")
	}
	if deleted, ended := scope.Stage.Deletes[ref.Address].Lookup(a.Row, a.Version); !ended || deleted.Version != a.Version || deleted.RepairStamp != headers.Header(a.Version).RepairStamp {
		t.Fatal("retained match lost its independently admitted exact deletion", deleted, ended)
	}
	if _, ended := scope.Stage.Deletes[ref.Address].Lookup(b.Row, b.Version); ended {
		t.Fatal("RI mutated the fresh sibling before its snapshot crosscheck")
	}
	if !storage.SameVersionRoot(store.Versions, current.Committed) || store.Headers.Header(a.Version).RepairStamp != headers.Header(a.Version).RepairStamp || headers.Header(a.Version).Successor != 0 {
		t.Fatal("RI conflict replaced committed roots or erased repair identity")
	}
	if headers.Header(a.Version).Xmax != 0 || !headers.Apply(a).ForcedFrozen {
		t.Fatal("later RI staging mutated captured repaired headers")
	}
	_ = scope.Finish(err)
	scope.Op.Done()
}
