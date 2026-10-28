package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestDeferredUniqueEventOwnership(t *testing.T) {
	e := engine.New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)")
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(a.Transactions)).Events) != 0 {
		t.Fatal("first uncontested unique key enlisted retrospective work")
	}
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	modes := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(b.Transactions))
	if len(modes.Events) != 1 || modes.Events[0].ForeignKey != nil {
		t.Fatal("missing concrete unique index event")
	}
	event := modes.Events[0]
	projection := e.Exclusions.OwnLocked(b.Transactions.Ticket)
	entries := projection.Entries[event.Index.Address]
	if len(entries) != 1 || event.Row != entries[0].Row.Row || event.Version != entries[0].Row.Version {
		t.Fatal("event lost exact published incarnation")
	}
	writes := b.Transactions.Writes[event.Parent.Address]
	if len(writes) != 1 || writes[0].Row != event.Row || writes[0].Version != event.Version {
		t.Fatal("dirty admission did not retain the actual staged physical incarnation", writes, event)
	}
	for resource, entry := range e.Locks.Inspect().Resources {
		if resource.Key() != nil {
			for _, grant := range entry.Holders {
				if grant.Mode == lock.UniqueClaim {
					t.Fatal("deferrable uniqueness acquired eager exclusive key claim")
				}
			}
		}
	}
	done := exclusionCommitWorker(t, b, br)
	exclusionCompletionQueued(t, e, a.Transactions.Ticket, b.Transactions.Ticket, done)
	transactionEnd(t, a, ar, false)
	exclusionCommitAwait(t, done)
	if e.Exclusions.CountLocked() != 0 {
		t.Fatal("completed unique projections retained")
	}
	uniqueClaimsEmpty(t, e)
}

func TestDeferredForeignKeyEventOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
	sessionSQL(t, s, "INSERT INTO p VALUES(1)")
	transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
	before := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if len(before.Events) != 1 || before.Events[0].ForeignKey == nil {
		t.Fatal("valid deferred child lost its trigger event at statement Finish")
	}
	payload := before.Events[0].ForeignKey
	if payload.Owner.ID() == 0 || payload.Next.Row != before.Events[0].Row || payload.Next.Version != before.Events[0].Version || payload.Next.IndexOutputs != nil || payload.Next.IndexAdmission != nil {
		t.Fatal("event retained command artifacts or lost actor/incarnation")
	}
	sessionSQL(t, s, "SAVEPOINT m")
	sessionSQL(t, s, "INSERT INTO c VALUES(9)")
	current := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if len(current.Events) != 2 || len(before.Events) != 1 || current.Events[0].ForeignKey != payload {
		t.Fatal("append mutated captured event owner")
	}
	sessionSQL(t, s, "ROLLBACK TO m")
	after := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if !reflect.DeepEqual(before, after) || after.Events[0].ForeignKey != payload {
		t.Fatal("savepoint lost exact original event payload")
	}
	sessionSQL(t, s, "RELEASE m")
	if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 1 {
		t.Fatal("RELEASE checked or discarded trigger work")
	}
	sessionSQL(t, s, "SET CONSTRAINTS fk IMMEDIATE")
	if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 0 {
		t.Fatal("successful due check retained obligation")
	}
	sessionSQL(t, s, "COMMIT")
}

func TestDeferredCompletionFailureOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
	ref := transactionBegin(t, s)
	sessionSQL(t, s, "CREATE TABLE private_definition(v INT)")
	sessionSQL(t, s, "INSERT INTO c VALUES(9)")
	op := transactionOperation(t, s)
	_, err := e.Commit(op, ref)
	op.Done()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
		t.Fatal(err)
	}
	if s.Transactions.Ref == ref || len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 0 || e.Exclusions.CountLocked() != 0 || core.EnginePreparedRef(e).Len() != 0 {
		t.Fatal("failed prepublication completion retained old incarnation")
	}
	enginetest.LockEmpty(t, e)
	next := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO p VALUES(9)")
	sessionSQL(t, s, "INSERT INTO c VALUES(9)")
	transactionEnd(t, s, next, true)
}

func TestDeferredSnapshotRootOwnership(t *testing.T) {
	for _, mutation := range []string{"DELETE FROM p", "UPDATE p SET id=2"} {
		t.Run(mutation, func(t *testing.T) { deferredSnapshotRootOwnership(t, mutation) })
	}
}

func deferredSnapshotRootOwnership(t *testing.T, mutation string) {
	e := engine.New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, a, "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
	sessionSQL(t, a, "INSERT INTO p VALUES(1)")
	parent := enginetest.RoleTableRef(t, e, "p")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, a, "SELECT id FROM p", 1)
	retained := a.Transactions.Roots[parent.Address]
	if len(retained.Indexes) != 1 {
		t.Fatal("stable snapshot did not retain its supporting unique root")
	}
	var rootAddress = enginetest.RoleTableRef(t, e, "p_pkey").Address
	original := retained.Indexes[rootAddress]
	if original.Root == nil || len(original.Root.Entries) != 1 || original.Definition == nil {
		t.Fatal("snapshot lacks coherent descriptor/root pair")
	}
	sessionSQL(t, b, mutation)
	sessionSQL(t, b, "CREATE UNIQUE INDEX later ON p(id)")
	if len(a.Transactions.Roots[parent.Address].Indexes) != 1 || a.Transactions.Roots[parent.Address].Indexes[rootAddress].Root != original.Root || len(original.Root.Entries) != 1 {
		t.Fatal("live publication mutated snapshot roots or retrospectively added an index")
	}
	// Own replacement repairs integrity; the retained root cannot force a
	// serialization failure after the current probe finds the final own key.
	sessionSQL(t, a, "SAVEPOINT m")
	sessionSQL(t, a, "INSERT INTO p VALUES(1)")
	sessionSQL(t, a, "INSERT INTO c VALUES(1)")
	sessionSQL(t, a, "ROLLBACK TO m")
	if a.Transactions.Roots[parent.Address].Indexes[rootAddress].Root != original.Root {
		t.Fatal("savepoint mutated captured index owner")
	}
	sessionSQL(t, a, "ALTER TABLE p ADD COLUMN note TEXT")
	if len(retained.Relation.Columns) != 1 || len(original.Root.Entries) != 1 || retained.Indexes[rootAddress].Root != original.Root {
		t.Fatal("column projection mutated original snapshot source")
	}
	sessionSQL(t, a, "INSERT INTO p(id) VALUES(1)")
	sessionSQL(t, a, "INSERT INTO c VALUES(1)")
	sessionSQL(t, a, "COMMIT")
	if len(original.Root.Entries) != 1 || retained.Indexes[rootAddress].Root != original.Root {
		t.Fatal("completion mutated original snapshot source")
	}
}
