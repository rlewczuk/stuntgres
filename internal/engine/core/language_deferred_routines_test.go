package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
)

func TestLanguageDeferredDefinitionOwnership(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TRUSTED LANGUAGE language_deferred HANDLER pg_catalog.plpgsql_call_handler")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_source(x integer) RETURNS integer LANGUAGE language_deferred AS $$BEGIN RETURN x+1; END$$")
	read := e.CatalogRead()
	var declaration *catalog.Procedure
	for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "language_source") {
		declaration = d
	}
	if declaration == nil || declaration.SQL == nil || declaration.SQL.Kind != proc.PLpgSQLBody || declaration.SQL.PL != nil || declaration.SQL.PLSyntax != nil {
		t.Fatal("missing legitimate source-only declaration")
	}
	definition := declaration.SQL
	formal := &definition.Formals[0]
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var first, second *catalog.Procedure
	if err == nil {
		first, err = e.PreparePLRoutineLocked(op, auth, declaration)
	}
	if err == nil {
		second, err = e.PreparePLRoutineLocked(op, auth, declaration)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if first == declaration || second == declaration || first == second || first.SQL == definition || first.SQL == second.SQL || first.SQL.PL == nil || first.SQL.PL == second.SQL.PL || first.SQL.PLSyntax == nil {
		t.Fatal("private invocation compilation reused published/shared executable")
	}
	if first.SQL.PL.ValidateDefinition(first.SQL) != nil || second.SQL.PL.ValidateDefinition(second.SQL) != nil {
		t.Fatal("private compiler did not produce genuine executable definitions")
	}
	if &first.SQL.Formals[0] == formal || &second.SQL.Formals[0] == formal || first.SQL.Position != definition.Position {
		t.Fatal("private compiler borrowed mutable formals or lost source coordinates")
	}
	if read.Get(declaration.Address()) != declaration || e.CatalogRead().Get(declaration.Address()) != declaration || declaration.SQL != definition || definition.PL != nil || definition.PLSyntax != nil || &definition.Formals[0] != formal {
		t.Fatal("invocation compilation mutated published source-only backing")
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, source := range []ids.ObjectAddress{declaration.Address(), declaration.Language} {
		destination, twice := source, source
		destination.ObjectID += 100000
		twice.ObjectID += 200000
		mapping[source], mapping[destination] = destination, twice
	}
	remap := stored.NewRoutineRemap(read, mapping, nil, nil, false)
	mapped, err := remap.Procedure(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
	if mapped == declaration || mapped.Address() != mapping[declaration.Address()] || mapped.Language != mapping[declaration.Language] || mapped.SQL.PL != nil || mapped.SQL.PLSyntax != nil || mapped.SQL.Source != definition.Source || mapped.SQL.Kind != proc.PLpgSQLBody {
		t.Fatal("source-only payload lost state or remapped identity more than once")
	}
	if declaration.Language == mapped.Language || read.Get(declaration.Address()) != declaration || definition.PL != nil {
		t.Fatal("destination remap changed source authority/backing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	previousContext := op.Ctx
	op.Ctx = ctx
	defer func() { op.Ctx = previousContext }()
	e.Mu.Lock()
	_, err = e.PreparePLRoutineLocked(op, auth, declaration)
	e.Mu.Unlock()
	if !errors.Is(err, context.Canceled) || declaration.SQL != definition || definition.PL != nil || definition.PLSyntax != nil {
		t.Fatal("canceled compilation changed published source", err)
	}
}
