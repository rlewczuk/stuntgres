package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestAlterDefaultPendingDescendantPublication(t *testing.T) {
	for _, action := range []string{"SET DEFAULT missing_default_function()", "DROP DEFAULT"} {
		t.Run(action, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
			enginetest.SessionSQL(t, s, "CREATE TABLE root(pid INT)")
			enginetest.SessionSQL(t, s, "CREATE TABLE child(CONSTRAINT fk FOREIGN KEY(pid) REFERENCES p INITIALLY DEFERRED) INHERITS(root)")
			enginetest.SessionSQL(t, s, "INSERT INTO p VALUES(1)")
			rootRef, childRef := enginetest.RoleTableRef(t, e, "root"), enginetest.RoleTableRef(t, e, "child")
			root, child := e.Catalog.State.MustRelation(rootRef.Address), e.Catalog.State.MustRelation(childRef.Address)
			rootStore, childStore := e.Storage.Relations[rootRef.Address], e.Storage.Relations[childRef.Address]
			enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "INSERT INTO child VALUES(1)")
			before := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if len(before.Events) != 1 || before.Events[0].Parent.Address != childRef.Address {
				t.Fatal("missing descendant event")
			}
			journal := s.Transactions.Catalog
			op := enginetest.TransactionOperation(t, s)
			_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE root ALTER COLUMN pid "+action), 0, nil)
			op.Done()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "55006" || diagnostic.Message != `cannot ALTER TABLE "child" because it has pending trigger events` {
				t.Fatal("descendant guard ran after default cooking", err)
			}
			after := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if s.Transactions.Catalog != journal || e.Catalog.State.MustRelation(rootRef.Address) != root || e.Catalog.State.MustRelation(childRef.Address) != child || e.Storage.Relations[rootRef.Address] != rootStore || e.Storage.Relations[childRef.Address] != childStore {
				t.Fatal("rejected recursive default published a partial successor")
			}
			if !reflect.DeepEqual(before, after) || &before.Events[0] != &after.Events[0] || before.Events[0].ForeignKey != after.Events[0].ForeignKey {
				t.Fatal("rejected recursive default consumed pending event")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
