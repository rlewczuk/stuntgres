package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// The phase witness is an actual completion request owned by the builder, not
// worker start, a timing delay, or descriptor visibility standing in for a queue.
func concurrentUnitCompletion(t *testing.T, e *Engine, owner *lock.Ticket, builder *Session) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.locks.Inspect()
		var found *lock.Request
		if entry := lockState.Resources[lock.CompletionOf(owner)]; entry != nil {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == builder.token && request.Mode == lock.TransactionCompletion {
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

func concurrentUnitIndex(t *testing.T, e *Engine, name string, ready, valid bool) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	namespace := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	index, ok := e.catalog.state.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Ready != ready || index.SQLIndex.Valid != valid || !index.SQLIndex.Live {
		t.Fatalf("index %s has unexpected phase: %#v", name, index)
	}
	return index, e.storage.IndexRoots[address]
}

func concurrentUnitError(t *testing.T, s *Session, query, code string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := s.Engine().Execute(op, statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("%s: %v; want %s", query, err, code)
	}
	return err
}

func concurrentUnitRootMatchesStorage(t *testing.T, e *Engine, index *catalog.Relation) {
	t.Helper()
	e.mu.Lock()
	parent := e.catalog.state.MustRelation(index.SQLIndex.Parent)
	snapshot := *e.storage.Relations[parent.Address()]
	root := e.storage.IndexRoots[index.Address()]
	e.mu.Unlock()
	expected, err := buildIndexRoot(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}, parent, index.SQLIndex, &snapshot, nil)
	if err != nil || !reflect.DeepEqual(root, expected) {
		t.Fatalf("installed logical root differs from actual live row-version build: %v", err)
	}
}

func TestConcurrentIndexU1RootPhasesAndVersions(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	owner := a.transactions.ticket
	table := roleTableRef(t, e, "t")
	e.mu.Lock()
	store := e.storage.Relations[table.Address]
	generation := store.Generation
	ends := store.Ends
	length := len(store.Versions)
	e.mu.Unlock()
	_, await := indexMaintenanceAsync(t, e, b, "CREATE INDEX CONCURRENTLY ix ON t(n DESC NULLS LAST)")
	concurrentUnitCompletion(t, e, owner, b)
	initial, root := concurrentUnitIndex(t, e, "ix", false, false)
	if root != nil {
		t.Fatal("initial phase fabricated a logical root")
	}
	e.mu.Lock()
	unchanged := e.storage.Relations[table.Address] == store && store.Generation == generation && store.Ends == ends && len(store.Versions) == length
	e.mu.Unlock()
	if !unchanged {
		t.Fatal("descriptor publication rewrote parent storage")
	}
	sessionSQL(t, c, "INSERT INTO t VALUES(3)")
	sessionSQL(t, c, "UPDATE t SET n=4 WHERE n=1")
	sessionSQL(t, c, "DELETE FROM t WHERE n=3")
	sessionSQL(t, a, "COMMIT")
	if err := await(); err != nil {
		t.Fatal(err)
	}
	healthy, root := concurrentUnitIndex(t, e, "ix", true, true)
	if healthy == initial || initial.SQLIndex.Ready || initial.SQLIndex.Valid || root == nil || !reflect.DeepEqual(btreeKeys(t, root), []string{"4", "2"}) {
		t.Fatal("phase mutated predecessor or missed committed row versions")
	}
	concurrentUnitRootMatchesStorage(t, e, healthy)
	sessionSQL(t, c, "UPDATE t SET n=5 WHERE n=2")
	healthy, _ = concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, healthy)
	lockEmpty(t, e)
}

func TestConcurrentIndexU4ReadinessAdmissionAndNoHealing(t *testing.T) {
	e := New()
	a, b, d, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT,p TEXT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1,'seed')")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, a, "INSERT INTO t VALUES(2,'first')")
	_, await := indexMaintenanceAsync(t, e, b, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n) INCLUDE(p)")
	concurrentUnitCompletion(t, e, a.transactions.ticket, b)
	initial, absent := concurrentUnitIndex(t, e, "ui", false, false)
	if absent != nil {
		t.Fatal("not-ready root present")
	}
	parent := e.CatalogRead().MustRelation(initial.SQLIndex.Parent)
	if len(parent.Constraints.Unique) != 0 || initial.SQLIndex.Constraint != nil || !initial.SQLIndex.Unique {
		t.Fatal("standalone index fabricated a parent constraint")
	}
	sessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, d, "INSERT INTO t VALUES(1,'grandfathered')")
	sessionSQL(t, a, "COMMIT")
	concurrentUnitCompletion(t, e, d.transactions.ticket, b)
	ready, before := concurrentUnitIndex(t, e, "ui", true, false)
	if !reflect.DeepEqual(btreeKeys(t, before), []string{"1", "2"}) || initial.SQLIndex.Ready {
		t.Fatal("first root/admission predecessor")
	}
	concurrentUnitError(t, c, "INSERT INTO t VALUES(1,'rejected')", "23505")
	sessionSQL(t, d, "COMMIT")
	err := await()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Message != `duplicate key value violates unique constraint "ui"` || diagnostic.Detail != "Key (n)=(1) already exists." {
		t.Fatal(err)
	}
	invalid, after := concurrentUnitIndex(t, e, "ui", true, false)
	if after != before || ready.SQLIndex.Valid {
		t.Fatal("pre-ready commit maintained root or failed validation healed it")
	}
	sessionSQL(t, c, "INSERT INTO t VALUES(3,'distinct')")
	sessionSQL(t, c, "UPDATE t SET n=4 WHERE n=3")
	sessionSQL(t, c, "UPDATE t SET p='payload' WHERE n=4")
	concurrentUnitError(t, c, "UPDATE t SET n=1 WHERE n=4", "23505")
	sessionSQL(t, c, "CREATE TABLE unrelated(n INT)")
	sessionSQL(t, c, "CREATE INDEX other ON unrelated(n)")
	concurrentUnitIndex(t, e, "ui", true, false)
	concurrentUnitError(t, c, "REINDEX INDEX ui", "23505")
	concurrentUnitIndex(t, e, "ui", true, false)
	sessionSQL(t, c, "ALTER INDEX ui RENAME TO renamed")
	renamed, _ := concurrentUnitIndex(t, e, "renamed", true, false)
	if renamed.Name() != "renamed" || invalid.Name() != "ui" || !renamed.SQLIndex.Unique || !invalid.SQLIndex.Unique || renamed.SQLIndex.Constraint != nil {
		t.Fatal("standalone rename corrupted immutable identity")
	}
	sessionSQL(t, c, "DELETE FROM t WHERE n=1")
	sessionSQL(t, c, "REINDEX TABLE t")
	repaired, _ := concurrentUnitIndex(t, e, "renamed", true, true)
	concurrentUnitRootMatchesStorage(t, e, repaired)
	lockEmpty(t, e)
}

func TestConcurrentIndexU4NotReadySkipsExpressionEvaluation(t *testing.T) {
	e := New()
	a, b, d, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	_, await := indexMaintenanceAsync(t, e, b, "CREATE INDEX CONCURRENTLY ix ON t((10/n))")
	concurrentUnitCompletion(t, e, a.transactions.ticket, b)
	sessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, d, "INSERT INTO t VALUES(0)")
	sessionSQL(t, a, "COMMIT")
	concurrentUnitCompletion(t, e, d.transactions.ticket, b)
	_, before := concurrentUnitIndex(t, e, "ix", true, false)
	concurrentUnitError(t, c, "INSERT INTO t VALUES(0)", "22012")
	sessionSQL(t, d, "COMMIT")
	var diagnostic *sqlstate.Error
	if err := await(); !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal(err)
	}
	_, after := concurrentUnitIndex(t, e, "ix", true, false)
	if before != after {
		t.Fatal("grandfathered commit evaluated/maintained the newly ready index")
	}
	sessionSQL(t, c, "DELETE FROM t WHERE n=0")
	sessionSQL(t, c, "REINDEX INDEX ix")
	repaired, _ := concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, repaired)
	lockEmpty(t, e)
}

func TestConcurrentIndexU5ComputedTupleAndClassClaims(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v TEXT,n INT,p TEXT)")
	sessionSQL(t, s, "INSERT INTO t VALUES('a',1,'seed')")
	sessionSQL(t, s, `CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE "C" bpchar_ops DESC NULLS LAST,(n+1),n,n) INCLUDE(p)`)
	index, root := concurrentUnitIndex(t, e, "ui", true, true)
	if len(root.Entries[0].Keys) != 4 || index.SQLIndex.Constraint != nil {
		t.Fatal("computed tuple or standalone identity")
	}
	concurrentUnitError(t, s, "INSERT INTO t VALUES('A ',1,'other')", "23505")
	concurrentUnitError(t, s, "INSERT INTO t VALUES('b',2,'x'),('B ',2,'y')", "23505")
	sessionSQL(t, s, "INSERT INTO t VALUES(NULL,1,'x'),(NULL,1,'y')")
	sessionSQL(t, s, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, s, "SAVEPOINT keep")
	sessionSQL(t, s, "INSERT INTO t VALUES('b',2,'x')")
	sessionSQL(t, s, "ROLLBACK TO keep")
	sessionSQL(t, s, "INSERT INTO t VALUES('B ',2,'y')")
	sessionSQL(t, s, "COMMIT")
	concurrentUnitRootMatchesStorage(t, e, index)
	sessionSQL(t, s, "ALTER TABLE t RENAME COLUMN v TO word")
	index, _ = concurrentUnitIndex(t, e, "ui", true, true)
	concurrentUnitError(t, s, "INSERT INTO t VALUES('a ',1,'again')", "23505")
	concurrentUnitRootMatchesStorage(t, e, index)
	lockEmpty(t, e)
}

func TestConcurrentIndexU4GenericShapePreservesInvalidRoots(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "readyinvalid"}[ready], func(t *testing.T) {
			e := New()
			s, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(n INT,extra TEXT,tail INT)")
			sessionSQL(t, s, "CREATE TABLE u(n INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,'x',10)")
			if ready {
				sessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				rows := sessionSQL(t, s, "SELECT n FROM u")
				rows.Rows.Close()
				cancel, await := indexMaintenanceAsync(t, e, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)")
				concurrentUnitCompletion(t, e, s.transactions.ticket, builder)
				cancel()
				if err := await(); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				sessionSQL(t, s, "ROLLBACK")
			} else {
				sessionSQL(t, s, "INSERT INTO t VALUES(1,'y',20)")
				concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
			}
			_, before := concurrentUnitIndex(t, e, "ui", ready, false)
			sessionSQL(t, s, "ALTER TABLE t DROP COLUMN tail")
			_, after := concurrentUnitIndex(t, e, "ui", ready, false)
			if before != after {
				t.Fatal("generic DROP rebuilt invalid root")
			}
			sessionSQL(t, s, "ALTER TABLE t ALTER extra TYPE VARCHAR(20)")
			_, after = concurrentUnitIndex(t, e, "ui", ready, false)
			if before != after {
				t.Fatal("unrelated TYPE rebuilt invalid root")
			}
			if !ready {
				concurrentUnitError(t, s, "ALTER TABLE t ALTER n TYPE BIGINT", "23505")
				concurrentUnitIndex(t, e, "ui", false, false)
				sessionSQL(t, s, "DELETE FROM t")
			}
			sessionSQL(t, s, "ALTER TABLE t ALTER n TYPE BIGINT")
			repaired, _ := concurrentUnitIndex(t, e, "ui", true, true)
			concurrentUnitRootMatchesStorage(t, e, repaired)
			lockEmpty(t, e)
		})
	}
}
