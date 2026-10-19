package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"testing"
)

type indexMaintenanceSnapshot struct {
	catalog *catalog.State
	roots   map[ids.ObjectAddress]*storage.IndexRoot
	stores  map[ids.ObjectAddress]*storage.Relation
}

func indexMaintenanceCapture(e *Engine) indexMaintenanceSnapshot {
	s := indexMaintenanceSnapshot{catalog: e.catalog.state, roots: make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.storage.IndexRoots)), stores: make(map[ids.ObjectAddress]*storage.Relation, len(e.storage.Relations))}
	for a, r := range e.storage.IndexRoots {
		s.roots[a] = r
	}
	for a, r := range e.storage.Relations {
		s.stores[a] = r
	}
	return s
}
func indexMaintenanceUnchanged(t *testing.T, e *Engine, before indexMaintenanceSnapshot) {
	t.Helper()
	if e.catalog.state != before.catalog || len(e.storage.IndexRoots) != len(before.roots) || len(e.storage.Relations) != len(before.stores) {
		t.Fatal("failed operation published catalog/storage/root state")
	}
	for a, r := range before.roots {
		if e.storage.IndexRoots[a] != r {
			t.Fatal("failed operation changed root", a)
		}
	}
	for a, r := range before.stores {
		if e.storage.Relations[a] != r {
			t.Fatal("failed operation changed table storage", a)
		}
	}
}

func TestIndexMaintenanceSelectedAndTableRoots(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", "CREATE INDEX ni ON t(n)", "CREATE INDEX si ON t(s DESC NULLS LAST,n)", "CREATE INDEX ei ON t(lower(s),(n+1),n DESC NULLS LAST)", "CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", "INSERT INTO q VALUES(1),(NULL)"} {
		run(t, e, q)
	}
	ni, oldN := btreeRoot(t, e, "ni")
	si, oldS := btreeRoot(t, e, "si")
	ei, oldE := btreeRoot(t, e, "ei")
	unique, oldU := btreeRoot(t, e, "u")
	parent := e.catalog.state.Get(ni.SQLIndex.Parent)
	store := e.storage.Relations[ni.SQLIndex.Parent]
	generation, ends := store.Generation, store.Ends
	run(t, e, "REINDEX INDEX ni")
	_, newN := btreeRoot(t, e, "ni")
	if newN == oldN || !reflect.DeepEqual(newN.Entries, oldN.Entries) || e.catalog.state.Get(ni.Address()) != ni || e.storage.IndexRoots[si.Address()] != oldS || e.storage.IndexRoots[ei.Address()] != oldE || e.storage.IndexRoots[unique.Address()] != oldU {
		t.Fatal("selected rebuild is not fresh, identity-preserving and sibling-isolated")
	}
	btreeWantKeys(t, e, "ni", "1", "2", "2", "3", "NULL")
	run(t, e, "ALTER INDEX ni RENAME TO new_ni")
	renamed, root := btreeRoot(t, e, "new_ni")
	if root != newN || renamed.Address() != ni.Address() || renamed.SQLIndex != ni.SQLIndex || e.catalog.state.Get(ni.SQLIndex.Parent) != parent {
		t.Fatal("simple rename rebuilt roots or changed parent/key identity")
	}
	run(t, e, "REINDEX TABLE t")
	if e.storage.IndexRoots[renamed.Address()] == newN || e.storage.IndexRoots[si.Address()] == oldS || e.storage.IndexRoots[ei.Address()] == oldE || e.storage.IndexRoots[unique.Address()] != oldU {
		t.Fatal("table rebuild is partial or touched another table")
	}
	for _, pair := range [][2]*storage.IndexRoot{{e.storage.IndexRoots[renamed.Address()], newN}, {e.storage.IndexRoots[si.Address()], oldS}, {e.storage.IndexRoots[ei.Address()], oldE}} {
		if !reflect.DeepEqual(pair[0].Entries, pair[1].Entries) {
			t.Fatal("rebuild reminted/dropped live duplicate versions")
		}
	}
	if e.catalog.state.Get(renamed.SQLIndex.Parent) != parent || e.storage.Relations[renamed.SQLIndex.Parent] != store || store.Generation != generation || store.Ends != ends {
		t.Fatal("reindex changed table definition/storage/membership")
	}
	run(t, e, "UPDATE t SET n=4 WHERE n=3")
	run(t, e, "DELETE FROM t WHERE n=1")
	run(t, e, "INSERT INTO t VALUES(0,'z')")
	btreeWantKeys(t, e, "new_ni", "0", "2", "2", "4", "NULL")
	if got := btreeKeys(t, oldN); !reflect.DeepEqual(got, []string{"1", "2", "2", "3", "NULL"}) {
		t.Fatal("retained predecessor mutated", got)
	}
	_, derived := btreeRoot(t, e, "ei")
	if got := expressionRootTuples(t, derived); !reflect.DeepEqual(got, [][]string{{"b", "3", "2"}, {"b", "3", "2"}, {"c", "5", "4"}, {"z", "1", "0"}, {"NULL", "NULL", "NULL"}}) {
		t.Fatal(got)
	}
	survivor := derived
	survivorStore := e.storage.Relations[renamed.SQLIndex.Parent]
	run(t, e, "DROP INDEX new_ni,si,new_ni CASCADE")
	if e.catalog.state.Get(renamed.Address()) != nil || e.catalog.state.Get(si.Address()) != nil || e.storage.IndexRoots[renamed.Address()] != nil || e.storage.IndexRoots[si.Address()] != nil || e.storage.IndexRoots[ei.Address()] != survivor || e.storage.Relations[renamed.SQLIndex.Parent] != survivorStore {
		t.Fatal("group drop did not isolate/deduplicate removals")
	}
	run(t, e, "CREATE INDEX new_ni ON t(n)")
	recreated, _ := btreeRoot(t, e, "new_ni")
	if recreated.Address() == renamed.Address() {
		t.Fatal("recreated name reused retired identity")
	}
	lockEmpty(t, e)
}

func TestIndexMaintenanceBackingRenameAndRoots(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT, CONSTRAINT check_name CHECK(n>0))", "INSERT INTO q VALUES(1),(NULL)", "CREATE INDEX sibling ON q(n DESC NULLS LAST)", "CREATE INDEX derived ON q((n+1))"} {
		run(t, e, q)
	}
	old, root := btreeRoot(t, e, "u")
	sibling, siblingRoot := btreeRoot(t, e, "sibling")
	derived, derivedRoot := btreeRoot(t, e, "derived")
	parent := e.catalog.state.MustRelation(old.SQLIndex.Parent)
	constraint := parent.Constraints.Unique[0]
	run(t, e, "ALTER INDEX u RENAME TO u_new")
	next, same := btreeRoot(t, e, "u_new")
	nextParent := e.catalog.state.MustRelation(old.SQLIndex.Parent)
	if same != root || next.Address() != old.Address() || next.SQLIndex.Constraint.OID != constraint.OID || next.SQLIndex.Constraint.Index != constraint.Index || next.SQLIndex.Constraint.Name != "u_new" || nextParent.Constraints.Unique[0].Name != "u_new" || nextParent.Constraints.Unique[0].OID != constraint.OID || e.storage.IndexRoots[sibling.Address()] != siblingRoot || e.storage.IndexRoots[derived.Address()] != derivedRoot {
		t.Fatal("backing rename did not preserve real identities/roots")
	}
	if parent.Constraints.Unique[0].Name != "u" || old.SQLIndex.Constraint.Name != "u" {
		t.Fatal("rename mutated captured descriptors")
	}
	before := indexMaintenanceCapture(e)
	for _, q := range []string{"INSERT INTO q VALUES(1)", "INSERT INTO q VALUES(NULL)"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "23505" || d.Constraint != "u_new" {
			t.Fatal("renamed constraint enforcement identity", d, err)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	run(t, e, "REINDEX TABLE q")
	if e.storage.IndexRoots[next.Address()] == root || e.storage.IndexRoots[sibling.Address()] == siblingRoot || e.storage.IndexRoots[derived.Address()] == derivedRoot {
		t.Fatal("table rebuild omitted UNIQUE/simple/derived roots")
	}
	btreeWantKeys(t, e, "u_new", "1", "NULL")
	before = indexMaintenanceCapture(e)
	for _, q := range []string{"DROP INDEX sibling,u_new CASCADE", "ALTER INDEX u_new RENAME TO check_name"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		if err == nil {
			t.Fatal("backing dependency/collision accepted", q)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	lockEmpty(t, e)
}

func TestIndexMaintenanceAtomicFailuresAndUnsupportedStorage(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1),(1),(NULL)", "CREATE INDEX ix ON t(n)"} {
		run(t, e, q)
	}
	before := indexMaintenanceCapture(e)
	for _, q := range []string{"DROP INDEX ix,missing", "DROP INDEX ix,t", "ALTER INDEX ix RENAME TO t", "ALTER INDEX ix SET(fillfactor=9)", "ALTER INDEX ix SET(fillfactor=80.0)", "ALTER INDEX ix RESET(unknown)", "ALTER INDEX ix RESET(fillfactor=80)", "REINDEX (parallel -1) INDEX ix", "REINDEX (concurrently false) INDEX ix", "ALTER INDEX ix SET TABLESPACE pg_default", "ALTER INDEX ix ALTER COLUMN 1 SET STATISTICS 10"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		if err == nil {
			t.Fatal("invalid/unavailable request succeeded", q)
		}
		indexMaintenanceUnchanged(t, e, before)
		lockEmpty(t, e)
	}
	run(t, e, "CREATE TABLE empty(n INT)")
	before = indexMaintenanceCapture(e)
	run(t, e, "REINDEX TABLE empty")
	indexMaintenanceUnchanged(t, e, before)
}

func TestIndexMaintenanceBuildCancellation(t *testing.T) {
	for _, at := range []int{25, 200} {
		t.Run(map[int]string{25: "extraction", 200: "sorting"}[at], func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES" + strings.TrimSuffix(strings.Repeat("(7),", 128), ","), "CREATE INDEX a ON t(n)", "CREATE INDEX b ON t(n DESC)"} {
				run(t, e, q)
			}
			before := indexMaintenanceCapture(e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			op.ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: int32(at)}
			_, err = e.Execute(op, statement(t, "REINDEX TABLE t"), 0, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("real build did not honor cancellation", err)
			}
			indexMaintenanceUnchanged(t, e, before)
			lockEmpty(t, e)
		})
	}
}
