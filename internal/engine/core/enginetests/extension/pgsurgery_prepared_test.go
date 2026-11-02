package extension_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// A real demanded immutable child distinguishes retained accepted fold backing
// from recomputation. The installed repair itself must remain executable.
const surgeryOwnedQuery = "SELECT heap_force_freeze('surgery_owned'::regclass, CASE WHEN COALESCE(-(7::integer),0)=-7 THEN '{}'::tid[] ELSE '{}'::tid[] END) AS repaired"

func TestPgsurgeryFreezeProviderPreparedOwnership(t *testing.T) {
	e := engine.New()
	surgeryInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE surgery_owned(n integer)")
	s := enginetest.StartSession(t, e, nil)
	ast, description := enginetest.PreparedDescription(t, e, s, surgeryOwnedQuery)
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	owner := description.Folds
	accepted := owner.Current()
	if !owner.Live() || accepted.Generation == 0 || len(accepted.Entries) == 0 {
		t.Fatal("missing actual accepted definition backing")
	}
	snapshot := slices.Clone(accepted.Entries)
	for i := range snapshot {
		snapshot[i].Inputs = slices.Clone(snapshot[i].Inputs)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	if description.Folds != owner || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeat execution replaced accepted owner/generation/backing")
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	local := enginetest.CloneFoldDescription(t, description)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = slices.Clone(publication.Group.Entries)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || len(owner.Current().Entries) != len(snapshot) || len(local.Folds.Current().Entries) != len(snapshot) || local.Folds.Current().Generation != accepted.Generation || &local.Folds.Current().Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("rejected publication changed accepted owner/generation/backing", err)
	}
	for i, want := range snapshot {
		if !enginetest.SamePublishedFold(owner.Current().Entries[i], want) {
			t.Fatal("rejected candidate changed retained immutable entry", i)
		}
	}
}

func TestPgsurgeryFreezeProviderProtocolOwnership(t *testing.T) {
	e := engine.New()
	surgeryInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE surgery_owned(n integer)")
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	parse := func() {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("surgery_definition", surgeryOwnedQuery, nil)
			return err
		})
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "surgery_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("surgery_definition")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing protocol definition owner")
	}
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated producer/children")
	}
	bind("surgery_first")
	first := registry.Binding("surgery_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || first.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("binding lost definition generation/backing")
	}
	assertGraph := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil {
			t.Fatal("missing accepted graph")
		}
		found := 0
		var visit func(expr.Expr)
		visit = func(node expr.Expr) {
			if call, ok := node.(plan.InstalledPgsurgeryExpr); ok {
				found++
				if call.CallRoutineReference() != call.Procedure || call.Ref.Address != call.Procedure.Address() || call.Properties().Volatility != expr.Volatile || !call.Properties().StageDependent || call.ResultUsage().Type.ID() != sqltype.Void {
					t.Fatal("accepted graph lost symbolic repair identity")
				}
				dependency := false
				for _, dep := range definition.Description.Analysis.Dependencies {
					dependency = dependency || dep.Ref == call.Ref && dep.Revision == call.Procedure.Revision()
				}
				if !dependency {
					t.Fatal("missing genuine executable analysis dependency")
				}
				for _, entry := range accepted.Entries {
					if entry.Callable == call.Procedure {
						t.Fatal("volatile repair producer folded")
					}
				}
			}
			plan.ExpressionChildren(node, visit)
		}
		for _, expression := range root.Expressions {
			visit(expression)
		}
		if found != 1 {
			t.Fatal("lost installed repair occurrence", found)
		}
	}
	if first.Resource == nil {
		t.Fatal("missing accepted query resource")
	}
	firstRoot := first.Resource.Root
	assertGraph(firstRoot)
	bind("surgery_second")
	second := registry.Binding("surgery_second")
	if second == nil || second == first || second.Entry.Description.Folds != owner || owner.Current().Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeat binding replaced accepted owner/backing")
	}
	assertGraph(firstRoot)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("surgery_definition") })
	if owner.Live() {
		t.Fatal("retired definition kept publication authority")
	}
	parse()
	bind("surgery_new")
	replacement := registry.Lookup("surgery_definition").Description.Folds
	replacementAccepted := replacement.Current()
	if replacement == owner || replacementAccepted.Generation == 0 || len(replacementAccepted.Entries) == 0 {
		t.Fatal("replacement reused retired owner")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("surgery_first", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if replacement.Current().Generation != replacementAccepted.Generation || &replacement.Current().Entries[0] != &replacementAccepted.Entries[0] || owner.Live() || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("old accepted binding changed retained backing or replacement owner")
	}
	assertGraph(firstRoot)
	for _, name := range []string{"surgery_first", "surgery_second", "surgery_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("surgery_definition") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
