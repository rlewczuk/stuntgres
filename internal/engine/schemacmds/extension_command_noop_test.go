package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

func TestExtensionCommandNoopPredecessor(t *testing.T) {
	e := engine.New()
	actor := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, actor, "CREATE EXTENSION unaccent")
	enginetest.SessionSQL(t, actor, "CREATE TYPE ec_peer_member AS ENUM ('a')")
	before := e.CatalogRead()
	address := before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	old, ok := before.Extension(address)
	if !ok {
		t.Fatal("missing installation")
	}
	op, err := actor.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unfinished same-schema predecessor proof"))
	stmt := enginetest.Statement(t, "ALTER EXTENSION unaccent SET SCHEMA public").(*parser.AlterExtension)
	if err := scope.Gate(stmt); err != nil {
		t.Fatal(err)
	}
	command := &core.EventCommand{Tag: "ALTER EXTENSION", CollectDDL: true}
	scope.EventCommand = command
	if _, err := schemacmds.ExecuteExtensionAlter(e, scope, stmt, 0); err != nil {
		t.Fatal(err)
	}
	scope.CompleteUtility()
	if scope.Completed || scope.Catalog == nil || scope.CatalogAuthority == nil || len(command.Commands) != 0 || e.PrivateCatalogLocked(scope.PendingCatalog()).Get(address) != old {
		t.Fatal("same-target command lost admission or minted a receipt")
	}
	if err := scope.FinishEventCommand(nil, nil); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, peer, "ALTER EXTENSION unaccent ADD TYPE ec_peer_member")
	winner := e.CatalogRead()
	if winner.Get(address) == old {
		t.Fatal("peer did not replace genuine predecessor")
	}
	var conflict catalog.Conflict
	if err := scope.Finish(nil); !errors.As(err, &conflict) {
		t.Fatal("same-target Finish accepted a stale extension predecessor", err)
	}
	if e.CatalogRead().State() != winner.State() || before.Get(address) != old || scope.EventCommand != nil || core.ActiveScopeOfLease(op.Lease) != nil {
		t.Fatal("rejected no-op changed winner/retained roots or kept authority")
	}
}
