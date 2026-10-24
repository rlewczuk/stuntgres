package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
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
	oldLeft, oldRight := e.storage.Relations[left.Address], e.storage.Relations[right.Address]
	scope := truncateUnitStage(t, e, s, "r")
	if len(scope.truncateOrZero().Relations) != 4 || len(scope.catalog.Storage) != 2 || e.storage.Relations[root.Address] != nil || e.storage.Relations[nested.Address] != nil {
		t.Fatal("virtual authorities became physical stores or a real leaf was omitted")
	}
	for _, ref := range []ids.ObjectRef{root, nested, left, right} {
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var hold lock.Hold
		ok := false
		if entry != nil {
			hold, ok = entry.Holders[scope.ticket]
		}
		if !ok || hold.Mode&lock.AccessExclusive == 0 {
			t.Fatal("missing canonical hierarchy AX", ref)
		}
	}
	if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	scope.op.Done()
	if e.storage.Relations[left.Address] != oldLeft || e.storage.Relations[right.Address] != oldRight || oldLeft.TruncateLineage != nil || oldRight.TruncateLineage != nil {
		t.Fatal("partition cancellation changed a leaf")
	}
	truncateUnitError(t, e, s, "TRUNCATE ONLY r", "42809")
	stmt := truncateUnitAST("left_leaf")
	truncateUnitExecute(t, e, s, stmt)
	if len(e.storage.Relations[left.Address].Versions) != 0 || e.storage.Relations[right.Address] != oldRight || len(oldRight.Versions) != 1 {
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
	writes, ticket := waiter.Transactions.Writes[p.Address], waiter.Transactions.Ticket
	done, cancel := truncateUnitAsync(t, e, waiter, truncateUnitAST("p", "q"))
	lockPending(t, e, 1)
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: p}]
	var mode lock.Mode
	if entry != nil {
		mode = entry.Holders[ticket].Mode
	}
	if mode&lock.AccessExclusive == 0 {
		t.Fatal("real prior grant was not upgraded")
	}
	cancel()
	lockAwait(t, done, context.Canceled)
	sessionSQL(t, waiter, "ROLLBACK TO preserve")
	foreignKeyHeldMode(t, e, p, 0, ticket, lock.AccessShare|lock.RowExclusive)
	if !storage.SameVersionRoot(waiter.Transactions.Writes[p.Address], writes) || waiter.Transactions.Catalog != nil && len(waiter.Transactions.Catalog.Storage) != 0 {
		t.Fatal("canceled upgrade lost earlier roots/left empty generation")
	}
	transactionEnd(t, holder, held, false)
	truncateUnitExecute(t, e, waiter, truncateUnitAST("p"))
	sessionSQL(t, waiter, "ROLLBACK TO preserve")
	if !storage.SameVersionRoot(waiter.Transactions.Writes[p.Address], writes) {
		t.Fatal("rollback-to did not restore roots after later success")
	}
	foreignKeyHeldMode(t, e, p, 0, ticket, lock.AccessShare|lock.RowExclusive)
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
			oldP, oldQ := e.storage.Relations[p.Address], e.storage.Relations[q.Address]
			writes := s.Transactions.Writes[p.Address]
			scope := truncateUnitStage(t, e, s, "p", "q")
			switch kind {
			case "storage":
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				next := *base.MustRelation(p.Address)
				next.ObjectRevision++
				replacement := &storage.Relation{Versions: oldP.Versions, Ends: oldP.Ends, Generation: oldP.Generation + 1}
				if err := stage.alterRelation(context.Background(), p, &next, oldP, replacement, len(oldP.Versions), oldP.Generation, oldP.Ends); err != nil {
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
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("epoch fence", err)
				}
			} else if err != (catalog.Conflict{}) {
				t.Fatal("final descriptor/storage fence", err)
			}
			if len(oldP.Versions) != 2 || len(oldQ.Versions) != 1 || oldP.TruncateLineage != nil || oldQ.TruncateLineage != nil {
				t.Fatal("late final veto changed committed predecessor")
			}
			if kind != "epoch" {
				sessionSQL(t, s, "ROLLBACK TO preserve")
				if !storage.SameVersionRoot(s.Transactions.Writes[p.Address], writes) || s.Transactions.Catalog != nil && len(s.Transactions.Catalog.Storage) != 0 {
					t.Fatal("final veto lost prior overlay roots")
				}
			}
			transactionEnd(t, s, tx, false)
			if kind == "epoch" {
				transactionRows(t, s, "SELECT id FROM p", 9)
			} else if len(e.storage.Relations[p.Address].Versions) != 2 || e.storage.Relations[q.Address] != oldQ {
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
	old := e.storage.Relations[p.Address]
	done, _ := truncateUnitAsync(t, e, waiter, truncateUnitAST("p"))
	lockPending(t, e, 1)
	base := e.CatalogRead()
	destination := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "other"})
	stage := e.BeginCatalogStage(base)
	// The same genuine group construction used by SET SCHEMA moves the table,
	// backing index and synchronized row/array types, without reminting storage.
	for _, name := range []string{"p", "p_pkey"} {
		ref := roleTableRef(t, e, name)
		next := *base.MustRelation(ref.Address)
		next.ObjectNamespace = destination
		next.ObjectRevision++
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	truncateUnitPublish(t, e, stage)
	transactionEnd(t, holder, tx, false)
	lockAwait(t, done, ids.ErrCatalogReplaced)
	if e.storage.Relations[p.Address] != old || old.TruncateLineage != nil || len(old.Versions) != 2 || e.catalog.State.Get(p.Address).Namespace() != destination {
		t.Fatal("queued name followed another namespace or undid real control publication")
	}
	transactionRows(t, waiter, "SELECT id FROM other.p ORDER BY id", 1, 2)
	lockEmpty(t, e)
}
