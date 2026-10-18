package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestTruncateGraphPartitions(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE r(id INT) PARTITION BY RANGE(id)", "CREATE TABLE nested PARTITION OF r FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(id)", "CREATE TABLE left_leaf PARTITION OF nested FOR VALUES FROM(0) TO(5)", "CREATE TABLE right_leaf PARTITION OF nested FOR VALUES FROM(5) TO(10)", "INSERT INTO r VALUES(1),(7)"} {
		sessionSQL(t, s, q)
	}
	root, nested := roleTableRef(t, e, "r"), roleTableRef(t, e, "nested")
	left, right := roleTableRef(t, e, "left_leaf"), roleTableRef(t, e, "right_leaf")
	oldLeft, oldRight := e.storage[left.Address], e.storage[right.Address]
	scope := truncateUnitStage(t, e, s, "r")
	if len(scope.truncateRelations) != 4 || len(scope.catalog.storage) != 2 || e.storage[root.Address] != nil || e.storage[nested.Address] != nil {
		t.Fatal("virtual authorities became physical stores or a real leaf was omitted")
	}
	for _, ref := range []ids.ObjectRef{root, nested, left, right} {
		e.locks.mu.Lock()
		entry := e.locks.entries[lockResource{ObjectRef: ref}]
		var hold relationHold
		ok := false
		if entry != nil {
			hold, ok = entry.holders[scope.ticket]
		}
		e.locks.mu.Unlock()
		if !ok || hold.Mode&accessExclusive == 0 {
			t.Fatal("missing canonical hierarchy AX", ref)
		}
	}
	if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	scope.op.Done()
	if e.storage[left.Address] != oldLeft || e.storage[right.Address] != oldRight || oldLeft.truncateLineage != nil || oldRight.truncateLineage != nil {
		t.Fatal("partition cancellation changed a leaf")
	}
	truncateUnitError(t, e, s, "TRUNCATE ONLY r", "42809")
	stmt := truncateUnitAST("left_leaf")
	truncateUnitExecute(t, e, s, stmt)
	if len(e.storage[left.Address].versions) != 0 || e.storage[right.Address] != oldRight || len(oldRight.versions) != 1 {
		t.Fatal("direct leaf changed sibling")
	}
	lockEmpty(t, e)
}
func TestTruncateLocksSavepoint(t *testing.T) {
	e, holder, p, _ := truncateUnitFixture(t)
	waiter := sessionStart(t, e, nil)
	held := transactionBegin(t, holder)
	sessionSQL(t, holder, "LOCK q IN ACCESS SHARE MODE")
	tx := transactionBegin(t, waiter)
	sessionSQL(t, waiter, "INSERT INTO p VALUES(3)")
	transactionRows(t, waiter, "SELECT id FROM p ORDER BY id", 1, 2, 3)
	sessionSQL(t, waiter, "SAVEPOINT preserve")
	writes, ticket := waiter.transactions.writes[p.Address], waiter.transactions.ticket
	done, cancel := truncateUnitAsync(t, e, waiter, truncateUnitAST("p", "q"))
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: p}]
	var mode relationLockMode
	if entry != nil {
		mode = entry.holders[ticket].Mode
	}
	e.locks.mu.Unlock()
	if mode&accessExclusive == 0 {
		t.Fatal("real prior grant was not upgraded")
	}
	cancel()
	lockAwait(t, done, context.Canceled)
	sessionSQL(t, waiter, "ROLLBACK TO preserve")
	foreignKeyHeldMode(t, e, p, 0, ticket, accessShare|rowExclusive)
	if !sameVersionRoot(waiter.transactions.writes[p.Address], writes) || waiter.transactions.catalog != nil && len(waiter.transactions.catalog.storage) != 0 {
		t.Fatal("canceled upgrade lost earlier roots/left empty generation")
	}
	transactionEnd(t, holder, held, false)
	truncateUnitExecute(t, e, waiter, truncateUnitAST("p"))
	sessionSQL(t, waiter, "ROLLBACK TO preserve")
	if !sameVersionRoot(waiter.transactions.writes[p.Address], writes) {
		t.Fatal("rollback-to did not restore roots after later success")
	}
	foreignKeyHeldMode(t, e, p, 0, ticket, accessShare|rowExclusive)
	transactionEnd(t, waiter, tx, false)
	lockEmpty(t, e)
}
func TestTruncateFinalVetoAdditional(t *testing.T) {
	for _, kind := range []string{"storage", "ACL", "epoch"} {
		t.Run(kind, func(t *testing.T) {
			e, s, p, q := truncateUnitFixture(t)
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "INSERT INTO p VALUES(3)")
			sessionSQL(t, s, "SAVEPOINT preserve")
			oldP, oldQ := e.storage[p.Address], e.storage[q.Address]
			writes := s.transactions.writes[p.Address]
			scope := truncateUnitStage(t, e, s, "p", "q")
			switch kind {
			case "storage":
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				next := *base.state.descriptors[p.Address].(*relationDescriptor)
				next.revision++
				replacement := &relationStorage{versions: oldP.versions, ends: oldP.ends, generation: oldP.generation + 1}
				if err := stage.alterRelation(context.Background(), p, &next, oldP, replacement, len(oldP.versions), oldP.generation, oldP.ends); err != nil {
					t.Fatal(err)
				}
				truncateUnitPublish(t, e, stage)
			case "ACL":
				if err := e.SetObjectACL(context.Background(), p, authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
			case "epoch":
				if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE p(id INT PRIMARY KEY); INSERT INTO p VALUES(9);")); err != nil {
					t.Fatal(err)
				}
			}
			err := scope.Finish(nil)
			scope.op.Done()
			if kind == "epoch" {
				if !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal("epoch fence", err)
				}
			} else if err != (CatalogConflict{}) {
				t.Fatal("final descriptor/storage fence", err)
			}
			if len(oldP.versions) != 2 || len(oldQ.versions) != 1 || oldP.truncateLineage != nil || oldQ.truncateLineage != nil {
				t.Fatal("late final veto changed committed predecessor")
			}
			if kind != "epoch" {
				sessionSQL(t, s, "ROLLBACK TO preserve")
				if !sameVersionRoot(s.transactions.writes[p.Address], writes) || s.transactions.catalog != nil && len(s.transactions.catalog.storage) != 0 {
					t.Fatal("final veto lost prior overlay roots")
				}
			}
			transactionEnd(t, s, tx, false)
			if kind == "epoch" {
				transactionRows(t, s, "SELECT id FROM p", 9)
			} else if len(e.storage[p.Address].versions) != 2 || e.storage[q.Address] != oldQ {
				t.Fatal("failed truncate overrode real host publication")
			}
			lockEmpty(t, e)
		})
	}
}
func TestTruncateAdmissionNamespace(t *testing.T) {
	e, holder, p, _ := truncateUnitFixture(t)
	sessionSQL(t, holder, "CREATE SCHEMA other")
	waiter := sessionStart(t, e, nil)
	sessionSQL(t, waiter, "SET search_path=public,other")
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "LOCK p IN ACCESS SHARE MODE")
	old := e.storage[p.Address]
	done, _ := truncateUnitAsync(t, e, waiter, truncateUnitAST("p"))
	lockPending(t, e, 1)
	base := e.CatalogRead()
	destination := base.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: "other"}]
	stage := e.BeginCatalogStage(base)
	// The same genuine group construction used by SET SCHEMA moves the table,
	// backing index and synchronized row/array types, without reminting storage.
	for _, name := range []string{"p", "p_pkey"} {
		ref := roleTableRef(t, e, name)
		next := *base.state.descriptors[ref.Address].(*relationDescriptor)
		next.namespace = destination
		next.revision++
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	truncateUnitPublish(t, e, stage)
	transactionEnd(t, holder, tx, false)
	lockAwait(t, done, ErrCatalogReplaced)
	if e.storage[p.Address] != old || old.truncateLineage != nil || len(old.versions) != 2 || e.state.descriptors[p.Address].Namespace() != destination {
		t.Fatal("queued name followed another namespace or undid real control publication")
	}
	transactionRows(t, waiter, "SELECT id FROM other.p ORDER BY id", 1, 2)
	lockEmpty(t, e)
}
