package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func pkeyOwnedCarrier(t *testing.T, root plan.Node) plan.InstalledDblinkGetPkeyExpr {
	t.Helper()
	var captured *plan.InstalledDblinkGetPkeyExpr
	plan.VisitExecutableQuery(root, func(plan.Node) {}, func(node expr.Expr) {
		plan.WalkExpression(node, func(child expr.Expr) {
			if call, ok := child.(plan.InstalledDblinkGetPkeyExpr); ok {
				copy := call
				captured = &copy
			}
		})
	})
	if captured == nil {
		t.Fatal("accepted graph lost genuine installed source")
	}
	return *captured
}

func TestDblinkGetPkeyOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION dblink")
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const source = "SELECT abs(-7) AS folded,k.* FROM dblink_get_pkey('absent_relation') AS k"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("pkey_owned", source, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("pkey_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing prepared definition")
	}
	owner, analysis := definition.Description.Folds, definition.Description.Analysis
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("parse evaluated a runtime relation or published folds")
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "pkey_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Analysis != analysis || b.Entry.Description.Folds != owner {
			t.Fatal("binding lost actual owner/private graph")
		}
		return b
	}
	first, second := bind("pkey_first"), bind("pkey_second")
	firstRoot, secondRoot := first.Resource.Root, second.Resource.Root
	firstResource, secondResource := first.Resource, second.Resource
	firstFolds, secondFolds := *first.Folds, *second.Folds
	beforeFirst, beforeSecond := pkeyOwnedCarrier(t, firstRoot), pkeyOwnedCarrier(t, secondRoot)
	view := e.CatalogRead()
	for _, n := range []plan.InstalledDblinkGetPkeyExpr{beforeFirst, beforeSecond} {
		if err := stored.ValidateInstalledDblinkGetPkey(n, view); err != nil {
			t.Fatal(err)
		}
		if n.Source == (expr.FoldSourceOccurrence{}) || n.Occurrence == 0 || n.CallRoutineReference() != n.Procedure || analysis.FoldCallables[n.Procedure.Address()] != n.Procedure || analysis.FoldTypes[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(n.Usage.Type.ID())}] != n.Usage.Type {
			t.Fatal("retained source lost original provenance/type/callable generation")
		}
	}
	if first == second || firstResource == secondResource || firstRoot == secondRoot || first.Birth == second.Birth || first.Folds == second.Folds {
		t.Fatal("portals share mutable owner/graph")
	}
	accepted := owner.Current()
	if len(accepted.Entries) == 0 || firstFolds.Generation != accepted.Generation || secondFolds.Generation != accepted.Generation || &firstFolds.Entries[0] != &accepted.Entries[0] || &secondFolds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("bindings did not retain immutable accepted fold backing")
	}
	savedEntries := slices.Clone(accepted.Entries)
	savedDependencies := slices.Clone(analysis.Dependencies)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		if err != nil || publication == nil {
			t.Fatal("missing real publication", err)
		}
		publication.Group.Entries = slices.Clone(publication.Group.Entries)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := publication.Accept(ctx, auth.Catalog, environment, view.Epoch()); !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("canceled publication accepted", err)
		}
	}()
	if registry.Lookup("pkey_owned") != definition || definition.Description.Analysis != analysis || definition.Description.Folds != owner || first.Resource != firstResource || second.Resource != secondResource || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot || !reflect.DeepEqual(savedDependencies, analysis.Dependencies) || !reflect.DeepEqual(beforeFirst, pkeyOwnedCarrier(t, firstRoot)) || !reflect.DeepEqual(beforeSecond, pkeyOwnedCarrier(t, secondRoot)) {
		t.Fatal("rejected publication mutated accepted artifacts")
	}
	if got := owner.Current(); got.Generation != accepted.Generation || &got.Entries[0] != &accepted.Entries[0] {
		t.Fatal("rejected publication replaced accepted backing")
	}
	for i, saved := range savedEntries {
		if !enginetest.SamePublishedFold(saved, owner.Current().Entries[i]) {
			t.Fatal("rejected publication changed immutable fold", i)
		}
	}
	other := engine.New()
	dblinkUnitInstall(t, other)
	destination := other.CatalogRead()
	mapper := stored.ViewQueryRemapper{
		Catalog: destination,
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			return destination.Owner().Domain.Ref(destination.Epoch(), ref.Address)
		},
		Type: func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			current, _ := destination.LookupType(d.ID())
			return current
		},
		Procedure: func(p *catalog.Procedure) (*catalog.Procedure, error) {
			current, ok := destination.Procedure(p.Address())
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			return current, nil
		},
	}
	mapped, err := mapper.Query(firstRoot)
	if err != nil {
		t.Fatal("destination-owned remap", err)
	}
	fresh := pkeyOwnedCarrier(t, mapped)
	if fresh.Ref.Domain() != destination.Owner().Domain || fresh.Procedure == beforeFirst.Procedure || fresh.Usage.Type == beforeFirst.Usage.Type || fresh.Usage.Type.ID() != beforeFirst.Usage.Type.ID() || fresh.Usage.Type != destination.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: fresh.Procedure.Result}).SQLType {
		t.Fatal("remap imported source nominal authority")
	}
	if err := stored.ValidateInstalledDblinkGetPkey(fresh, destination); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeFirst, pkeyOwnedCarrier(t, firstRoot)) {
		t.Fatal("remap mutated source graph")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("pkey_owned") })
	if owner.Live() || first.Retired || second.Retired || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
		t.Fatal("definition retirement mutated retained portals")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("pkey_first") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("pkey_second") })
	if !first.Retired || !second.Retired {
		t.Fatal("portal close retained live producer")
	}
}
