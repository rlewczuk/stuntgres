package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func parameterSharedUnitFixture(t *testing.T) (*core.Engine, *core.Engine) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE grant_reader LOGIN")
	enginetest.SessionSQL(t, s, "CREATE DATABASE parameter_peer")
	b, err := e.SelectDatabase(context.Background(), "parameter_peer")
	if err != nil {
		t.Fatal(err)
	}
	return e, b
}

func parameterSharedUnitGrant(t *testing.T, e *core.Engine, name string, revoke bool) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	request := parameterSpaceRequest("parameter", name)
	request.Revoke = revoke
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
}

func parameterSharedUnitDescriptor(e *core.Engine, name string) *catalog.Parameter {
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: name})
	parameter, _ := e.Catalog.State.Get(address).(*catalog.Parameter)
	return parameter
}

func parameterSharedUnitCoherent(t *testing.T, name string, engines ...*core.Engine) *catalog.Parameter {
	t.Helper()
	first := parameterSharedUnitDescriptor(engines[0], name)
	for _, e := range engines[1:] {
		if parameterSharedUnitDescriptor(e, name) != first {
			t.Fatal("shared descriptor backing diverged", name)
		}
		if e.Catalog.State.ParameterIncarnation() != engines[0].Catalog.State.ParameterIncarnation() {
			t.Fatal("shared incarnation diverged")
		}
	}
	return first
}

// Capture an actual statement journal before Finish; this is not a copied SQL
// result. Its admission and immutable predecessor survive until final publication.
func parameterSharedUnitPending(t *testing.T, e *core.Engine, name string) (*core.StatementScope, *session.Operation, func()) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	scope, err := e.StartStatement(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	_, err = scope.Execute(parameterSpaceRequest("parameter", name), 0, nil)
	if err != nil {
		scope.Finish(err)
		done()
		t.Fatal(err)
	}
	if scope.PendingCatalog() == nil || !scope.PendingCatalog().ParameterWritten {
		scope.Finish(context.Canceled)
		done()
		t.Fatal("missing real pending parameter journal")
	}
	return scope, op, done
}
