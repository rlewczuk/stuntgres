package indexcmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

type indexMaintenanceSnapshot struct {
	catalog *catalog.State
	roots   map[ids.ObjectAddress]*storage.IndexRoot
	stores  map[ids.ObjectAddress]*storage.Relation
}

func indexMaintenanceCapture(e *core.Engine) indexMaintenanceSnapshot {
	s := indexMaintenanceSnapshot{catalog: e.Catalog.State, roots: make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots)), stores: make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))}
	for a, r := range e.Storage.IndexRoots {
		s.roots[a] = r
	}
	for a, r := range e.Storage.Relations {
		s.stores[a] = r
	}
	return s
}
func indexMaintenanceUnchanged(t *testing.T, e *core.Engine, before indexMaintenanceSnapshot) {
	t.Helper()
	if e.Catalog.State != before.catalog || len(e.Storage.IndexRoots) != len(before.roots) || len(e.Storage.Relations) != len(before.stores) {
		t.Fatal("failed operation published catalog/storage/root state")
	}
	for a, r := range before.roots {
		if e.Storage.IndexRoots[a] != r {
			t.Fatal("failed operation changed root", a)
		}
	}
	for a, r := range before.stores {
		if e.Storage.Relations[a] != r {
			t.Fatal("failed operation changed table storage", a)
		}
	}
}

func TestIndexMaintenanceSelectedAndTableRoots(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", "CREATE INDEX ni ON t(n)", "CREATE INDEX si ON t(s DESC NULLS LAST,n)", "CREATE INDEX ei ON t(lower(s),(n+1),n DESC NULLS LAST)", "CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", "INSERT INTO q VALUES(1),(NULL)"} {
		enginetest.Run(t, e, q)
	}
	ni, oldN := btreeRoot(t, e, "ni")
	si, oldS := btreeRoot(t, e, "si")
	ei, oldE := btreeRoot(t, e, "ei")
	unique, oldU := btreeRoot(t, e, "u")
	parent := e.Catalog.State.Get(ni.SQLIndex.Parent)
	store := e.Storage.Relations[ni.SQLIndex.Parent]
	generation, ends := store.Generation, store.Ends
	enginetest.Run(t, e, "REINDEX INDEX ni")
	_, newN := btreeRoot(t, e, "ni")
	if newN == oldN || !reflect.DeepEqual(newN.Entries, oldN.Entries) || e.Catalog.State.Get(ni.Address()) != ni || e.Storage.IndexRoots[si.Address()] != oldS || e.Storage.IndexRoots[ei.Address()] != oldE || e.Storage.IndexRoots[unique.Address()] != oldU {
		t.Fatal("selected rebuild is not fresh, identity-preserving and sibling-isolated")
	}
	btreeWantKeys(t, e, "ni", "1", "2", "2", "3", "NULL")
	enginetest.Run(t, e, "ALTER INDEX ni RENAME TO new_ni")
	renamed, root := btreeRoot(t, e, "new_ni")
	if root != newN || renamed.Address() != ni.Address() || renamed.SQLIndex != ni.SQLIndex || e.Catalog.State.Get(ni.SQLIndex.Parent) != parent {
		t.Fatal("simple rename rebuilt roots or changed parent/key identity")
	}
	enginetest.Run(t, e, "REINDEX TABLE t")
	if e.Storage.IndexRoots[renamed.Address()] == newN || e.Storage.IndexRoots[si.Address()] == oldS || e.Storage.IndexRoots[ei.Address()] == oldE || e.Storage.IndexRoots[unique.Address()] != oldU {
		t.Fatal("table rebuild is partial or touched another table")
	}
	for _, pair := range [][2]*storage.IndexRoot{{e.Storage.IndexRoots[renamed.Address()], newN}, {e.Storage.IndexRoots[si.Address()], oldS}, {e.Storage.IndexRoots[ei.Address()], oldE}} {
		if !reflect.DeepEqual(pair[0].Entries, pair[1].Entries) {
			t.Fatal("rebuild reminted/dropped live duplicate versions")
		}
	}
	if e.Catalog.State.Get(renamed.SQLIndex.Parent) != parent || e.Storage.Relations[renamed.SQLIndex.Parent] != store || store.Generation != generation || store.Ends != ends {
		t.Fatal("reindex changed table definition/storage/membership")
	}
	enginetest.Run(t, e, "UPDATE t SET n=4 WHERE n=3")
	enginetest.Run(t, e, "DELETE FROM t WHERE n=1")
	enginetest.Run(t, e, "INSERT INTO t VALUES(0,'z')")
	btreeWantKeys(t, e, "new_ni", "0", "2", "2", "4", "NULL")
	if got := btreeKeys(t, oldN); !reflect.DeepEqual(got, []string{"1", "2", "2", "3", "NULL"}) {
		t.Fatal("retained predecessor mutated", got)
	}
	_, derived := btreeRoot(t, e, "ei")
	if got := expressionRootTuples(t, derived); !reflect.DeepEqual(got, [][]string{{"b", "3", "2"}, {"b", "3", "2"}, {"c", "5", "4"}, {"z", "1", "0"}, {"NULL", "NULL", "NULL"}}) {
		t.Fatal(got)
	}
	survivor := derived
	survivorStore := e.Storage.Relations[renamed.SQLIndex.Parent]
	enginetest.Run(t, e, "DROP INDEX new_ni,si,new_ni CASCADE")
	if e.Catalog.State.Get(renamed.Address()) != nil || e.Catalog.State.Get(si.Address()) != nil || e.Storage.IndexRoots[renamed.Address()] != nil || e.Storage.IndexRoots[si.Address()] != nil || e.Storage.IndexRoots[ei.Address()] != survivor || e.Storage.Relations[renamed.SQLIndex.Parent] != survivorStore {
		t.Fatal("group drop did not isolate/deduplicate removals")
	}
	enginetest.Run(t, e, "CREATE INDEX new_ni ON t(n)")
	recreated, _ := btreeRoot(t, e, "new_ni")
	if recreated.Address() == renamed.Address() {
		t.Fatal("recreated name reused retired identity")
	}
	enginetest.LockEmpty(t, e)
}

func TestIndexMaintenanceBackingRenameAndRoots(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT, CONSTRAINT check_name CHECK(n>0))", "INSERT INTO q VALUES(1),(NULL)", "CREATE INDEX sibling ON q(n DESC NULLS LAST)", "CREATE INDEX derived ON q((n+1))"} {
		enginetest.Run(t, e, q)
	}
	old, root := btreeRoot(t, e, "u")
	sibling, siblingRoot := btreeRoot(t, e, "sibling")
	derived, derivedRoot := btreeRoot(t, e, "derived")
	parent := e.Catalog.State.MustRelation(old.SQLIndex.Parent)
	constraint := parent.Constraints.Unique[0]
	enginetest.Run(t, e, "ALTER INDEX u RENAME TO u_new")
	next, same := btreeRoot(t, e, "u_new")
	nextParent := e.Catalog.State.MustRelation(old.SQLIndex.Parent)
	if same != root || next.Address() != old.Address() || next.SQLIndex.Constraint.OID != constraint.OID || next.SQLIndex.Constraint.Index != constraint.Index || next.SQLIndex.Constraint.Name != "u_new" || nextParent.Constraints.Unique[0].Name != "u_new" || nextParent.Constraints.Unique[0].OID != constraint.OID || e.Storage.IndexRoots[sibling.Address()] != siblingRoot || e.Storage.IndexRoots[derived.Address()] != derivedRoot {
		t.Fatal("backing rename did not preserve real identities/roots")
	}
	if parent.Constraints.Unique[0].Name != "u" || old.SQLIndex.Constraint.Name != "u" {
		t.Fatal("rename mutated captured descriptors")
	}
	before := indexMaintenanceCapture(e)
	for _, q := range []string{"INSERT INTO q VALUES(1)", "INSERT INTO q VALUES(NULL)"} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "23505" || d.Constraint != "u_new" {
			t.Fatal("renamed constraint enforcement identity", d, err)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	enginetest.Run(t, e, "REINDEX TABLE q")
	if e.Storage.IndexRoots[next.Address()] == root || e.Storage.IndexRoots[sibling.Address()] == siblingRoot || e.Storage.IndexRoots[derived.Address()] == derivedRoot {
		t.Fatal("table rebuild omitted UNIQUE/simple/derived roots")
	}
	btreeWantKeys(t, e, "u_new", "1", "NULL")
	before = indexMaintenanceCapture(e)
	for _, q := range []string{"DROP INDEX sibling,u_new CASCADE", "ALTER INDEX u_new RENAME TO check_name"} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil)
		if err == nil {
			t.Fatal("backing dependency/collision accepted", q)
		}
		indexMaintenanceUnchanged(t, e, before)
	}
	enginetest.LockEmpty(t, e)
}

func TestIndexMaintenanceAtomicFailuresAndStorageBoundaries(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1),(1),(NULL)", "CREATE INDEX ix ON t(n)"} {
		enginetest.Run(t, e, q)
	}
	before := indexMaintenanceCapture(e)
	for _, q := range []string{"DROP INDEX ix,missing", "DROP INDEX ix,t", "ALTER INDEX ix RENAME TO t", "ALTER INDEX ix SET(fillfactor=9)", "ALTER INDEX ix SET(fillfactor=80,deduplicate_items='bad')", "ALTER INDEX ix RESET(fillfactor=80)", "REINDEX (parallel -1) INDEX ix", "REINDEX (concurrently false) INDEX ix", "ALTER INDEX ix SET TABLESPACE pg_default", "ALTER INDEX ix ALTER COLUMN 1 SET STATISTICS 10"} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil)
		if err == nil {
			t.Fatal("invalid/unavailable request succeeded", q)
		}
		indexMaintenanceUnchanged(t, e, before)
		enginetest.LockEmpty(t, e)
	}
	enginetest.Run(t, e, "CREATE TABLE empty(n INT)")
	before = indexMaintenanceCapture(e)
	enginetest.Run(t, e, "REINDEX TABLE empty")
	indexMaintenanceUnchanged(t, e, before)
}

func TestIndexMaintenanceBuildCancellation(t *testing.T) {
	for _, at := range []int{25, 200} {
		t.Run(map[int]string{25: "extraction", 200: "sorting"}[at], func(t *testing.T) {
			e := engine.New()
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES" + strings.TrimSuffix(strings.Repeat("(7),", 128), ","), "CREATE INDEX a ON t(n)", "CREATE INDEX b ON t(n DESC)"} {
				enginetest.Run(t, e, q)
			}
			before := indexMaintenanceCapture(e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			op.Ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: int32(at)}
			_, err = e.Execute(op, enginetest.Statement(t, "REINDEX TABLE t"), 0, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("real build did not honor cancellation", err)
			}
			indexMaintenanceUnchanged(t, e, before)
			enginetest.LockEmpty(t, e)
		})
	}
}
