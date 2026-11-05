package extension_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func evictionPreparedOwner(t *testing.T, e *core.Engine) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	// A demanded immutable child supplies distinguishing accepted fold backing;
	// the eviction invocation itself must remain volatile and symbolic.
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT (pg_buffercache_evict(COALESCE(-(7::integer),0)+8)).buffer_evicted")
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	owner := d.Folds
	accepted := owner.Current()
	if !owner.Live() || accepted.Generation == 0 || len(accepted.Entries) == 0 {
		t.Fatal("missing real accepted definition generation")
	}
	snapshot := slices.Clone(accepted.Entries)
	for i := range snapshot {
		snapshot[i].Inputs = slices.Clone(snapshot[i].Inputs)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	if d.Folds != owner || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("unchanged execution replaced accepted immutable backing")
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	local := enginetest.CloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	scope.Finish(err)
	op.Done()
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || len(owner.Current().Entries) != len(snapshot) || len(local.Folds.Current().Entries) != len(snapshot) || local.Folds.Current().Generation != accepted.Generation || &local.Folds.Current().Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("rejected publication changed accepted owner/generation/backing", err)
	}
	for i, want := range snapshot {
		if !enginetest.SamePublishedFold(owner.Current().Entries[i], want) {
			t.Fatal("rejected publication mutated accepted immutable entry", i)
		}
	}
}

func evictionProtocolOwner(t *testing.T, e *core.Engine) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := "SELECT pg_buffercache_evict(COALESCE(-(7::integer),0)+8) AS flags"
	parse := func() {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("eviction_definition", query, nil)
			return err
		})
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "eviction_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("eviction_definition")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual protocol definition owner")
	}
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated producer or children")
	}
	bind("eviction_first")
	first := registry.Binding("eviction_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || first.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("accepted binding lost definition generation/backing")
	}
	assertGraph := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil {
			t.Fatal("missing accepted private graph")
		}
		found := 0
		var visit func(expr.Expr)
		visit = func(node expr.Expr) {
			if call, ok := node.(plan.InstalledPgBuffercacheEvictionExpr); ok {
				found++
				if call.Kind != functions.EvictBuffer || call.CallRoutineReference() != call.Procedure || call.Ref.Address != call.Procedure.Address() || call.Properties().Volatility != expr.Volatile || !call.Properties().StageDependent || call.ResultUsage().Type.CompositeLen() != 2 {
					t.Fatal("accepted graph lost symbolic operation authority")
				}
				dependency := false
				for _, dep := range definition.Description.Analysis.Dependencies {
					dependency = dependency || dep.Ref == call.Ref && dep.Revision == call.Procedure.Revision()
				}
				if !dependency {
					t.Fatal("analysis omitted genuine executable dependency")
				}
				for _, entry := range accepted.Entries {
					if entry.Callable == call.Procedure {
						t.Fatal("volatile eviction producer was folded")
					}
				}
			}
			plan.ExpressionChildren(node, visit)
		}
		for _, expression := range root.Expressions {
			visit(expression)
		}
		if found != 1 {
			t.Fatal("private graph lost selected eviction occurrence", found)
		}
	}
	if first.Resource == nil {
		t.Fatal("accepted binding omitted query resource")
	}
	firstRoot := first.Resource.Root
	assertGraph(firstRoot)
	bind("eviction_second")
	second := registry.Binding("eviction_second")
	if second == nil || second == first || second.Entry.Description.Folds != owner || owner.Current().Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeated binding replaced accepted owner/backing")
	}
	assertGraph(firstRoot)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("eviction_definition") })
	if owner.Live() {
		t.Fatal("retired definition kept publication authority")
	}
	parse()
	bind("eviction_new")
	replacement := registry.Lookup("eviction_definition").Description.Folds
	replacementAccepted := replacement.Current()
	if replacement == owner || replacementAccepted.Generation == 0 || len(replacementAccepted.Entries) == 0 {
		t.Fatal("replacement reused retired definition authority")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("eviction_first", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if replacement.Current().Generation != replacementAccepted.Generation || &replacement.Current().Entries[0] != &replacementAccepted.Entries[0] || owner.Live() || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("old accepted binding published into replacement or changed retained backing")
	}
	assertGraph(firstRoot)
	for _, name := range []string{"eviction_first", "eviction_second", "eviction_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("eviction_definition") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
