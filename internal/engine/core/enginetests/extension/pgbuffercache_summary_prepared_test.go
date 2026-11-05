package extension_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func summaryPreparedOwner(t *testing.T, e *core.Engine, kind functions.PgBuffercacheAggregateKind) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	// A demanded immutable child supplies distinguishing accepted fold backing;
	// the summary invocation itself must remain volatile and symbolic.
	ast, d := enginetest.PreparedDescription(t, e, s, summaryPreparedQuery(kind))
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

func summaryProtocolOwner(t *testing.T, e *core.Engine, kind functions.PgBuffercacheAggregateKind) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := summaryPreparedQuery(kind)
	parse := func() {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("summary_definition", query, nil)
			return err
		})
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "summary_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("summary_definition")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual protocol definition owner")
	}
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated producer or children")
	}
	bind("summary_first")
	first := registry.Binding("summary_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || first.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("accepted binding lost definition generation/backing")
	}
	assertGraph := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil {
			t.Fatal("missing accepted private graph")
		}
		var calls []plan.InstalledPgBuffercacheAggregateExpr
		var visit func(expr.Expr)
		visit = func(node expr.Expr) {
			if call, ok := node.(plan.InstalledPgBuffercacheAggregateExpr); ok {
				seen := false
				for _, previous := range calls { seen = seen || call.SameSourceOccurrence(previous) }
				if !seen { calls = append(calls, call) }
				if call.Kind != kind || call.CallRoutineReference() != call.Procedure || call.Ref.Address != call.Procedure.Address() || call.Properties().Volatility != expr.Volatile || !call.Properties().StageDependent || call.ResultUsage().Type.CompositeLen() != summaryPreparedFields(kind) {
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
						t.Fatal("volatile summary producer was folded")
					}
				}
			}
			plan.ExpressionChildren(node, visit)
		}
		plan.VisitCTEQuery(root, func(plan.Node) {}, visit, false)
		if len(calls) != 1 {
			t.Fatal("private graph lost selected summary occurrence", len(calls))
		}
	}
	if first.Resource == nil {
		t.Fatal("accepted binding omitted query resource")
	}
	firstRoot := first.Resource.Root
	assertGraph(firstRoot)
	bind("summary_second")
	second := registry.Binding("summary_second")
	if second == nil || second == first || second.Entry.Description.Folds != owner || owner.Current().Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeated binding replaced accepted owner/backing")
	}
	assertGraph(firstRoot)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("summary_definition") })
	if owner.Live() {
		t.Fatal("retired definition kept publication authority")
	}
	parse()
	bind("summary_new")
	replacement := registry.Lookup("summary_definition").Description.Folds
	replacementAccepted := replacement.Current()
	if replacement == owner || replacementAccepted.Generation == 0 || len(replacementAccepted.Entries) == 0 {
		t.Fatal("replacement reused retired definition authority")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("summary_first", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if replacement.Current().Generation != replacementAccepted.Generation || &replacement.Current().Entries[0] != &replacementAccepted.Entries[0] || owner.Live() || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("old accepted binding published into replacement or changed retained backing")
	}
	assertGraph(firstRoot)
	for _, name := range []string{"summary_first", "summary_second", "summary_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("summary_definition") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
func summaryPreparedQuery(kind functions.PgBuffercacheAggregateKind) string {
 name := "pg_buffercache_summary"
 if kind.Set() { name = "pg_buffercache_usage_counts" }
 return "SELECT "+name+"() AS observation,COALESCE(-(7::integer),0) AS marker"
}
func summaryPreparedFields(kind functions.PgBuffercacheAggregateKind) int {
 if kind.Set() { return 4 }; return 5
}
func TestPgBuffercacheSummaryPreparedOwnership(t *testing.T) {
 for _, kind := range []functions.PgBuffercacheAggregateKind{functions.BuffercacheSummary, functions.BuffercacheUsageCounts} {
  e := engine.New()
  buffercacheInstalled(t,e)
  summaryPreparedOwner(t,e,kind)
  summaryProtocolOwner(t,e,kind)
 }
}
