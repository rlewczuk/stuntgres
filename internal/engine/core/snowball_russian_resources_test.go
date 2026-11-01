package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func russianProtocolOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE russian_resource (v integer)")
	enginetest.SessionSQL(t, s, "INSERT INTO russian_resource VALUES (0)")
	tx := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, tx, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("russian_owned", "SELECT ts_lexize('russian_stem'::regdictionary,'машины'::text) AS lexemes,1/v AS later FROM russian_resource", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("russian_owned")
	if definition == nil || definition.Description.Folds == nil || definition.Description.Analysis == nil {
		t.Fatal("missing actual definition")
	}
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated provider")
	}
	bind := func(name string) *core.PreparedBinding {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "russian_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil {
			t.Fatal("missing live binding")
		}
		return b
	}
	first, second := bind("russian_first"), bind("russian_second")
	accepted := owner.Current()
	if len(accepted.Entries) == 0 || first.Entry.Description.Folds != owner || second.Entry.Description.Folds != owner || first.Folds.Generation != accepted.Generation || second.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] || &second.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("actual definition generation/backing not shared")
	}
	fold := enginetest.PreparedFoldEntry(t, definition.Description, 1009, "{машин}")
	retained := enginetest.SnapshotFoldDescription(first.Entry.Description)
	firstFolds := first.Folds
	graph, sibling := first.Resource.Root, second.Resource.Root
	if first.Resource == second.Resource || graph == sibling {
		t.Fatal("bindings share mutable graph/resource")
	}
	assertGraph := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil || len(root.Expressions) != 2 {
			t.Fatal("private graph shape")
		}
		literal, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok || !sqltype.SameDatum(literal.Value, fold.Value) || literal.ResultUsage() != fold.Result || literal.Source != fold.Source || literal.Occurrence != fold.Occurrence || literal.RoutineReference != nil {
			t.Fatal("graph lost exact accepted typed datum/provenance")
		}
	}
	assertGraph(graph)
	assertGraph(sibling)
	firstProducer := core.PortalProducer(core.ProtocolPortalOf(first))
	secondProducer := core.PortalProducer(core.ProtocolPortalOf(second))
	if firstProducer == nil || secondProducer == nil || firstProducer == secondProducer {
		t.Fatal("live producer ownership")
	}
	firstState, secondState := core.CursorState(firstProducer), core.CursorState(secondProducer)
	firstContext := firstState.Evaluation(context.Background())
	secondContext := secondState.Evaluation(context.Background())
	if firstContext.Query == nil || secondContext.Query == nil || firstContext.Query == secondContext.Query || firstContext.Query.Source == secondContext.Query.Source {
		t.Fatal("private invocation contexts share frame")
	}
	func() {
		op, err := s.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
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
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
		current := owner.Current()
		if !errors.Is(err, context.Canceled) || publication.Group.Sealed || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || first.Resource.Root != graph || second.Resource.Root != sibling {
			t.Fatal("rejected publication changed accepted owners", err)
		}
	}()
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("russian_owned") })
	if owner.Live() || first.Retired || second.Retired || first.Resource.Root != graph || second.Resource.Root != sibling {
		t.Fatal("definition retirement detached independent portals")
	}
	// Execute the real producer. Its later row-dependent division fails only
	// after Bind accepted the Russian fold; terminal cleanup owns only its graph.
	op, _, err := s.AcquireCommand(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	_, err = scope.ExecuteProtocolPortal("russian_first", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
	failure := scope.Finish(err)
	op.Done()
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal("missing later real execution failure", failure)
	}
	if first.Resource.Root != nil || first.Folds != nil {
		t.Fatal("failed producer retained live graph/fold group")
	}
	if second.Resource.Root != sibling {
		t.Fatal("failed invocation detached sibling")
	}
	assertGraph(graph)
	assertGraph(sibling)
	// The definition is retired and the failed portal has detached its live
	// folds. Inspect the accepted backing saved while the owner was live,
	// rather than asking that retired definition for next-use eligibility.
	if !enginetest.SamePublishedFold(fold, enginetest.PreparedFoldEntry(t, retained, 1009, "{машин}")) || !sqltype.SameDatum(firstFolds.Entries[0].Value, accepted.Entries[0].Value) || firstFolds.Generation != accepted.Generation || &firstFolds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("terminal cleanup mutated retained immutable backing")
	}
}
