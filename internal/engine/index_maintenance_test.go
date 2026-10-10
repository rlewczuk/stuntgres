package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

type indexMaintenanceSnapshot struct {
	catalog *catalogState
	roots   map[ObjectAddress]*logicalIndexRoot
	stores  map[ObjectAddress]*relationStorage
}

func indexMaintenanceCapture(e *Engine) indexMaintenanceSnapshot {
	s := indexMaintenanceSnapshot{catalog: e.state, roots: make(map[ObjectAddress]*logicalIndexRoot, len(e.indexRoots)), stores: make(map[ObjectAddress]*relationStorage, len(e.storage))}
	for a, r := range e.indexRoots {
		s.roots[a] = r
	}
	for a, r := range e.storage {
		s.stores[a] = r
	}
	return s
}
func indexMaintenanceUnchanged(t *testing.T, e *Engine, before indexMaintenanceSnapshot) {
	t.Helper()
	if e.state != before.catalog || len(e.indexRoots) != len(before.roots) || len(e.storage) != len(before.stores) {
		t.Fatal("failed operation published catalog/storage/root state")
	}
	for a, r := range before.roots {
		if e.indexRoots[a] != r {
			t.Fatal("failed operation changed root", a)
		}
	}
	for a, r := range before.stores {
		if e.storage[a] != r {
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
	parent := e.state.descriptors[ni.sqlIndex.parent]
	store := e.storage[ni.sqlIndex.parent]
	generation, ends := store.generation, store.ends
	run(t, e, "REINDEX INDEX ni")
	_, newN := btreeRoot(t, e, "ni")
	if newN == oldN || !reflect.DeepEqual(newN.entries, oldN.entries) || e.state.descriptors[ni.address] != ni || e.indexRoots[si.address] != oldS || e.indexRoots[ei.address] != oldE || e.indexRoots[unique.address] != oldU {
		t.Fatal("selected rebuild is not fresh, identity-preserving and sibling-isolated")
	}
	btreeWantKeys(t, e, "ni", "1", "2", "2", "3", "NULL")
	run(t, e, "ALTER INDEX ni RENAME TO new_ni")
	renamed, root := btreeRoot(t, e, "new_ni")
	if root != newN || renamed.address != ni.address || renamed.sqlIndex != ni.sqlIndex || e.state.descriptors[ni.sqlIndex.parent] != parent {
		t.Fatal("simple rename rebuilt roots or changed parent/key identity")
	}
	run(t, e, "REINDEX TABLE t")
	if e.indexRoots[renamed.address] == newN || e.indexRoots[si.address] == oldS || e.indexRoots[ei.address] == oldE || e.indexRoots[unique.address] != oldU {
		t.Fatal("table rebuild is partial or touched another table")
	}
	for _, pair := range [][2]*logicalIndexRoot{{e.indexRoots[renamed.address], newN}, {e.indexRoots[si.address], oldS}, {e.indexRoots[ei.address], oldE}} {
		if !reflect.DeepEqual(pair[0].entries, pair[1].entries) {
			t.Fatal("rebuild reminted/dropped live duplicate versions")
		}
	}
	if e.state.descriptors[renamed.sqlIndex.parent] != parent || e.storage[renamed.sqlIndex.parent] != store || store.generation != generation || store.ends != ends {
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
	survivorStore := e.storage[renamed.sqlIndex.parent]
	run(t, e, "DROP INDEX new_ni,si,new_ni CASCADE")
	if e.state.descriptors[renamed.address] != nil || e.state.descriptors[si.address] != nil || e.indexRoots[renamed.address] != nil || e.indexRoots[si.address] != nil || e.indexRoots[ei.address] != survivor || e.storage[renamed.sqlIndex.parent] != survivorStore {
		t.Fatal("group drop did not isolate/deduplicate removals")
	}
	run(t, e, "CREATE INDEX new_ni ON t(n)")
	recreated, _ := btreeRoot(t, e, "new_ni")
	if recreated.address == renamed.address {
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
	parent := e.state.descriptors[old.sqlIndex.parent].(*relationDescriptor)
	constraint := parent.uniqueConstraints[0]
	run(t, e, "ALTER INDEX u RENAME TO u_new")
	next, same := btreeRoot(t, e, "u_new")
	nextParent := e.state.descriptors[old.sqlIndex.parent].(*relationDescriptor)
	if same != root || next.address != old.address || next.sqlIndex.constraint.OID != constraint.OID || next.sqlIndex.constraint.Index != constraint.Index || next.sqlIndex.constraint.Name != "u_new" || nextParent.uniqueConstraints[0].Name != "u_new" || nextParent.uniqueConstraints[0].OID != constraint.OID || e.indexRoots[sibling.address] != siblingRoot || e.indexRoots[derived.address] != derivedRoot {
		t.Fatal("backing rename did not preserve real identities/roots")
	}
	if parent.uniqueConstraints[0].Name != "u" || old.sqlIndex.constraint.Name != "u" {
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
	if e.indexRoots[next.address] == root || e.indexRoots[sibling.address] == siblingRoot || e.indexRoots[derived.address] == derivedRoot {
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
