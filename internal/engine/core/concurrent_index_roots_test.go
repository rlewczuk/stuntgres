package core_test

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

// The phase witness is an actual completion request owned by the builder, not
// worker start, a timing delay, or descriptor visibility standing in for a queue.
func concurrentUnitCompletion(t *testing.T, e *core.Engine, owner *lock.Ticket, builder *session.Session) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		var found *lock.Request
		if entry := lockState.Resources[lock.CompletionOf(owner)]; entry != nil {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == builder.SessionToken && request.Mode == lock.TransactionCompletion {
					found = request.Request
					break
				}
			}
		}
		if found != nil {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatal("builder did not journal exact transaction completion wait")
		}
		runtime.Gosched()
	}
}

func concurrentUnitIndex(t *testing.T, e *core.Engine, name string, ready, valid bool) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	index, ok := e.Catalog.State.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Ready != ready || index.SQLIndex.Valid != valid || !index.SQLIndex.Live {
		t.Fatalf("index %s has unexpected phase: %#v", name, index)
	}
	return index, e.Storage.IndexRoots[address]
}

func concurrentUnitError(t *testing.T, s *session.Session, query, code string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("%s: %v; want %s", query, err, code)
	}
	return err
}

func concurrentUnitRootMatchesStorage(t *testing.T, e *core.Engine, idx *catalog.Relation) {
	t.Helper()
	e.Mu.Lock()
	parent := e.Catalog.State.MustRelation(idx.SQLIndex.Parent)
	snapshot := *e.Storage.Relations[parent.Address()]
	root := e.Storage.IndexRoots[idx.Address()]
	e.Mu.Unlock()
	expected, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, &snapshot, nil)
	if err != nil || !reflect.DeepEqual(root, expected) {
		t.Fatalf("installed logical root differs from actual live row-version build: %v", err)
	}
}

func TestConcurrentIndexU1RootPhasesAndVersions(t *testing.T) {
	e := engine.New()
	a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	owner := a.Transactions.Ticket
	table := enginetest.RoleTableRef(t, e, "t")
	e.Mu.Lock()
	store := e.Storage.Relations[table.Address]
	generation := store.Generation
	ends := store.Ends
	length := len(store.Versions)
	e.Mu.Unlock()
	_, await := indexMaintenanceAsync(t, e, b, "CREATE INDEX CONCURRENTLY ix ON t(n DESC NULLS LAST)")
	concurrentUnitCompletion(t, e, owner, b)
	initial, root := concurrentUnitIndex(t, e, "ix", false, false)
	if root != nil {
		t.Fatal("initial phase fabricated a logical root")
	}
	e.Mu.Lock()
	unchanged := e.Storage.Relations[table.Address] == store && store.Generation == generation && store.Ends == ends && len(store.Versions) == length
	e.Mu.Unlock()
	if !unchanged {
		t.Fatal("descriptor publication rewrote parent storage")
	}
	enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3)")
	enginetest.SessionSQL(t, c, "UPDATE t SET n=4 WHERE n=1")
	enginetest.SessionSQL(t, c, "DELETE FROM t WHERE n=3")
	enginetest.SessionSQL(t, a, "COMMIT")
	if err := await(); err != nil {
		t.Fatal(err)
	}
	healthy, root := concurrentUnitIndex(t, e, "ix", true, true)
	if healthy == initial || initial.SQLIndex.Ready || initial.SQLIndex.Valid || root == nil || !reflect.DeepEqual(btreeKeys(t, root), []string{"4", "2"}) {
		t.Fatal("phase mutated predecessor or missed committed row versions")
	}
	concurrentUnitRootMatchesStorage(t, e, healthy)
	enginetest.SessionSQL(t, c, "UPDATE t SET n=5 WHERE n=2")
	healthy, _ = concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, healthy)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU4ReadinessAdmissionAndNoHealing(t *testing.T) {
	e := engine.New()
	a, b, d, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT,p TEXT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1,'seed')")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2,'first')")
	_, await := indexMaintenanceAsync(t, e, b, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n) INCLUDE(p)")
	concurrentUnitCompletion(t, e, a.Transactions.Ticket, b)
	initial, absent := concurrentUnitIndex(t, e, "ui", false, false)
	if absent != nil {
		t.Fatal("not-ready root present")
	}
	parent := e.CatalogRead().MustRelation(initial.SQLIndex.Parent)
	if len(parent.Constraints.Unique) != 0 || initial.SQLIndex.Constraint != nil || !initial.SQLIndex.Unique {
		t.Fatal("standalone index fabricated a parent constraint")
	}
	enginetest.SessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, d, "INSERT INTO t VALUES(1,'grandfathered')")
	enginetest.SessionSQL(t, a, "COMMIT")
	concurrentUnitCompletion(t, e, d.Transactions.Ticket, b)
	ready, before := concurrentUnitIndex(t, e, "ui", true, false)
	if !reflect.DeepEqual(btreeKeys(t, before), []string{"1", "2"}) || initial.SQLIndex.Ready {
		t.Fatal("first root/admission predecessor")
	}
	concurrentUnitError(t, c, "INSERT INTO t VALUES(1,'rejected')", "23505")
	enginetest.SessionSQL(t, d, "COMMIT")
	err := await()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Message != `duplicate key value violates unique constraint "ui"` || diagnostic.Detail != "Key (n)=(1) already exists." {
		t.Fatal(err)
	}
	invalid, after := concurrentUnitIndex(t, e, "ui", true, false)
	if after != before || ready.SQLIndex.Valid {
		t.Fatal("pre-ready commit maintained root or failed validation healed it")
	}
	enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,'distinct')")
	enginetest.SessionSQL(t, c, "UPDATE t SET n=4 WHERE n=3")
	enginetest.SessionSQL(t, c, "UPDATE t SET p='payload' WHERE n=4")
	concurrentUnitError(t, c, "UPDATE t SET n=1 WHERE n=4", "23505")
	enginetest.SessionSQL(t, c, "CREATE TABLE unrelated(n INT)")
	enginetest.SessionSQL(t, c, "CREATE INDEX other ON unrelated(n)")
	concurrentUnitIndex(t, e, "ui", true, false)
	concurrentUnitError(t, c, "REINDEX INDEX ui", "23505")
	concurrentUnitIndex(t, e, "ui", true, false)
	enginetest.SessionSQL(t, c, "ALTER INDEX ui RENAME TO renamed")
	renamed, _ := concurrentUnitIndex(t, e, "renamed", true, false)
	if renamed.Name() != "renamed" || invalid.Name() != "ui" || !renamed.SQLIndex.Unique || !invalid.SQLIndex.Unique || renamed.SQLIndex.Constraint != nil {
		t.Fatal("standalone rename corrupted immutable identity")
	}
	enginetest.SessionSQL(t, c, "DELETE FROM t WHERE n=1")
	enginetest.SessionSQL(t, c, "REINDEX TABLE t")
	repaired, _ := concurrentUnitIndex(t, e, "renamed", true, true)
	concurrentUnitRootMatchesStorage(t, e, repaired)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU4NotReadySkipsExpressionEvaluation(t *testing.T) {
	e := engine.New()
	a, b, d, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	_, await := indexMaintenanceAsync(t, e, b, "CREATE INDEX CONCURRENTLY ix ON t((10/n))")
	concurrentUnitCompletion(t, e, a.Transactions.Ticket, b)
	enginetest.SessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, d, "INSERT INTO t VALUES(0)")
	enginetest.SessionSQL(t, a, "COMMIT")
	concurrentUnitCompletion(t, e, d.Transactions.Ticket, b)
	_, before := concurrentUnitIndex(t, e, "ix", true, false)
	concurrentUnitError(t, c, "INSERT INTO t VALUES(0)", "22012")
	enginetest.SessionSQL(t, d, "COMMIT")
	var diagnostic *sqlstate.Error
	if err := await(); !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal(err)
	}
	_, after := concurrentUnitIndex(t, e, "ix", true, false)
	if before != after {
		t.Fatal("grandfathered commit evaluated/maintained the newly ready index")
	}
	enginetest.SessionSQL(t, c, "DELETE FROM t WHERE n=0")
	enginetest.SessionSQL(t, c, "REINDEX INDEX ix")
	repaired, _ := concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, repaired)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU5ComputedTupleAndClassClaims(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v TEXT,n INT,p TEXT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES('a',1,'seed')")
	enginetest.SessionSQL(t, s, `CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE "C" bpchar_ops DESC NULLS LAST,(n+1),n,n) INCLUDE(p)`)
	index, root := concurrentUnitIndex(t, e, "ui", true, true)
	if len(root.Entries[0].Keys) != 4 || index.SQLIndex.Constraint != nil {
		t.Fatal("computed tuple or standalone identity")
	}
	concurrentUnitError(t, s, "INSERT INTO t VALUES('A ',1,'other')", "23505")
	concurrentUnitError(t, s, "INSERT INTO t VALUES('b',2,'x'),('B ',2,'y')", "23505")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(NULL,1,'x'),(NULL,1,'y')")
	enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, s, "SAVEPOINT keep")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES('b',2,'x')")
	enginetest.SessionSQL(t, s, "ROLLBACK TO keep")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES('B ',2,'y')")
	enginetest.SessionSQL(t, s, "COMMIT")
	concurrentUnitRootMatchesStorage(t, e, index)
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME COLUMN v TO word")
	index, _ = concurrentUnitIndex(t, e, "ui", true, true)
	concurrentUnitError(t, s, "INSERT INTO t VALUES('a ',1,'again')", "23505")
	concurrentUnitRootMatchesStorage(t, e, index)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU4GenericShapePreservesInvalidRoots(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "readyinvalid"}[ready], func(t *testing.T) {
			e := engine.New()
			s, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT,extra TEXT,tail INT)")
			enginetest.SessionSQL(t, s, "CREATE TABLE u(n INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,'x',10)")
			if ready {
				enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				rows := enginetest.SessionSQL(t, s, "SELECT n FROM u")
				rows.Rows.Close()
				cancel, await := indexMaintenanceAsync(t, e, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)")
				concurrentUnitCompletion(t, e, s.Transactions.Ticket, builder)
				cancel()
				if err := await(); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				enginetest.SessionSQL(t, s, "ROLLBACK")
			} else {
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,'y',20)")
				concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
			}
			_, before := concurrentUnitIndex(t, e, "ui", ready, false)
			enginetest.SessionSQL(t, s, "ALTER TABLE t DROP COLUMN tail")
			_, after := concurrentUnitIndex(t, e, "ui", ready, false)
			if before != after {
				t.Fatal("generic DROP rebuilt invalid root")
			}
			enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER extra TYPE VARCHAR(20)")
			_, after = concurrentUnitIndex(t, e, "ui", ready, false)
			if before != after {
				t.Fatal("unrelated TYPE rebuilt invalid root")
			}
			if !ready {
				concurrentUnitError(t, s, "ALTER TABLE t ALTER n TYPE BIGINT", "23505")
				concurrentUnitIndex(t, e, "ui", false, false)
				enginetest.SessionSQL(t, s, "DELETE FROM t")
			}
			enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER n TYPE BIGINT")
			repaired, _ := concurrentUnitIndex(t, e, "ui", true, true)
			concurrentUnitRootMatchesStorage(t, e, repaired)
			enginetest.LockEmpty(t, e)
		})
	}
}
