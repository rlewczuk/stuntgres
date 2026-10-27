package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func transactionPreparedOwnership(t *testing.T) {
	t.Helper()
	e := engine.New()
	sess := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, sess, "CREATE PROCEDURE tx_owned(IN x integer) LANGUAGE plpgsql AS $$BEGIN COMMIT;END$$")
	cycle := sess.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, sess, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("tx_definition", "CALL tx_owned(COALESCE(-(7::integer),0))", nil)
		return err
	})
	registry := core.PayloadOf(sess).PreparedSQL
	definition := registry.Lookup("tx_definition")
	owner := definition.Description.Folds
	if owner == nil || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse retained executable folds")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "tx_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("tx_old")
	old := registry.Binding("tx_old")
	if old == nil || old.Resource == nil || old.Resource.Root != nil || old.Resource.Closer == nil {
		t.Fatal("CALL did not retain its portal close capability")
	}
	resource := old.Resource
	accepted := owner.Current()
	if len(accepted.Entries) == 0 || accepted.Generation == 0 {
		t.Fatal("Bind did not publish real argument folds")
	}
	var declaration *catalog.Procedure
	for _, candidate := range definition.Description.Analysis.FoldCallables {
		if candidate.Name() == "tx_owned" {
			declaration = candidate
		}
	}
	if declaration == nil || declaration.SQL == nil || declaration.SQL.PL == nil {
		t.Fatal("description lost its checked procedure declaration")
	}
	retained, program := declaration.SQL, declaration.SQL.PL
	formal := &retained.Formals[0]
	variable := &program.Variables[0]
	// Capture under admission, but assert only after the helper's Finish has
	// released it: t.Fatal inside its callback would deadlock session teardown.
	rotated := false
	command(func(scope *core.StatementScope) error {
		before := scope.TxRef
		_, err := scope.ExecuteProtocolPortal("tx_old", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		rotated = err == nil && scope.TxRef != before && scope.Owner.State == session.TxImplicit
		return err
	})
	if !rotated {
		t.Fatal("execution did not rotate the real portal owner")
	}
	if old.Retired || old.Resource != resource || resource.Root != nil || owner.Current().Generation != accepted.Generation || &old.Folds.Entries[0] != &accepted.Entries[0] || declaration.SQL != retained || retained.PL != program || &retained.Formals[0] != formal || &program.Variables[0] != variable {
		t.Fatal("rotation mutated retained declaration/program/fold backing")
	}
	bind("tx_again")
	if definition.Description.Folds != owner || registry.Binding("tx_again").Folds.Generation != accepted.Generation {
		t.Fatal("successor lost accepted definition ownership")
	}
	op, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
	e.Mu.Unlock()
	op.Done()
	if err != nil || publication == nil {
		t.Fatal("real definition attempt missing", err)
	}
	enginetest.SessionSQL(t, sess, "ALTER PROCEDURE tx_owned(integer) SECURITY DEFINER")
	bind("tx_new")
	current := registry.Binding("tx_new")
	currentDeclaration := current.Entry.Description.Analysis.FoldCallables[declaration.Address()]
	if current.Folds.Generation <= accepted.Generation || current.Resource == resource || current.Resource.Root != nil || currentDeclaration == nil || currentDeclaration == declaration || !currentDeclaration.SecurityDefiner {
		t.Fatal("attribute change did not advance accepted generation")
	}
	op, err = sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err = e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	environment, err = e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	err = publication.Accept(op.Context(), auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	op.Done()
	if !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed || owner.Current().Generation != current.Folds.Generation || old.Resource != resource || resource.Root != nil || &old.Folds.Entries[0] != &accepted.Entries[0] || retained.PL != program || &retained.Formals[0] != formal {
		t.Fatal("rejected attempt changed accepted ownership", err)
	}
	for _, name := range []string{"tx_old", "tx_again", "tx_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("tx_definition") })
	op, _, err = sess.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
}
