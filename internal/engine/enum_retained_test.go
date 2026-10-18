package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Instrument a real enum producer's argument to distinguish capture from demand.
type enumCapturedArgument struct {
	expr.Info
	value sqltype.Value
	calls *int
}

func (e enumCapturedArgument) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	(*e.calls)++
	return e.value, nil
}

func TestEnumRetainedProducerEnvironment(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('z','q')"))
	mood := domainUnitType(t, e.CatalogRead(), "mood").SQLType
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	members := mood.EnumMembers()
	e.mu.Lock()
	scope.owner.catalog = newCatalogChanges()
	scope.owner.catalog.enumUnsafe = map[int64]bool{members[1].ID: true}
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sqltype.BindEnumSupport("enum_last", mood.Usage(), mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	safe, err := mood.Input(context.Background(), "z", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"default", "generation", "check"} {
		t.Run(route, func(t *testing.T) {
			calls := 0
			producer := functions.Enum{Info: expr.Info{Usage: mood.Usage()}, Plan: plan, Args: []expr.Expr{enumCapturedArgument{Info: expr.Info{Usage: mood.Usage()}, value: sqltype.Null(mood), calls: &calls}}}
			nested := conditionalExpression{Info: expr.Info{Usage: mood.Usage()}, kind: parser.Coalesce, args: []expr.Expr{literalExpression{Info: expr.Info{Usage: mood.Usage()}, value: safe}, producer}}
			e.mu.Lock()
			unsafeRoot := scope.owner.catalog
			scope.owner.catalog = newCatalogChanges()
			b := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, nil)
			b.queryAuth = &auth
			var defaults []expr.Expr
			var generations map[int32]columnGeneration
			var checks []checkConstraint
			switch route {
			case "default":
				defaults = []expr.Expr{nested}
			case "generation":
				generations = map[int32]columnGeneration{1: {planned: nested}}
			case "check":
				checks = []checkConstraint{{expression: nested}}
			}
			err := b.captureRetainedWriteInput(defaults, generations, checks)
			lazyNamingOnly := b.input.TypeDiagnosticName != nil && b.input.EnumMemberSafe == nil
			scope.owner.catalog = unsafeRoot
			if err == nil {
				captureEnumOperationSafety(op, &b.input)
				err = e.captureEnumOperationInput(auth, &b.input)
			}
			e.mu.Unlock()
			if err != nil || !lazyNamingOnly || calls != 0 || b.input.TypeDiagnosticName == nil || b.input.EnumMemberSafe == nil {
				t.Fatal("capture evaluated producer or lost environment", calls, err)
			}
			if b.input.TypeDiagnosticName(mood.ID()) != "mood" || !b.input.EnumMemberSafe(mood.ID(), members[0].ID) || b.input.EnumMemberSafe(mood.ID(), members[1].ID) {
				t.Fatal("incoherent catalog/provenance capture")
			}
			eval := &EvalContext{Context: context.Background(), Input: b.input}
			value, err := nested.Eval(eval)
			if err != nil || !sqltype.SameDatum(value, safe) || calls != 0 {
				t.Fatal("unused producer demanded", calls, err)
			}
			_, err = producer.Eval(eval)
			d, ok := err.(*sqlstate.Error)
			if !ok || d.Code != "55P04" || d.Message != `unsafe use of new value "q" of enum type mood` || d.Hint != "New enum values must be committed before they can be used." || calls != 1 {
				t.Fatal("demanded producer bypassed safety", calls, err)
			}
		})
	}
}

func TestEnumOperationSafetyCapture(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('z','q')"))
	mood := domainUnitType(t, e.CatalogRead(), "mood").SQLType
	members := mood.EnumMembers()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	e.mu.Lock()
	defer e.mu.Unlock()
	root := newCatalogChanges()
	root.enumUnsafe = map[int64]bool{members[1].ID: true}
	scope.owner.catalog = root
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	b := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	if err := e.captureEnumOperationInput(auth, &b.input); err != nil {
		t.Fatal(err)
	}
	original := b.input
	if original.TypeDiagnosticName == nil || original.TypeDiagnosticName(mood.ID()) != "mood" || original.EnumMemberSafe == nil || !original.EnumMemberSafe(mood.ID(), members[0].ID) || original.EnumMemberSafe(mood.ID(), members[1].ID) {
		t.Fatal("operation capture lost admitted naming or exact member safety")
	}
	// A preexisting coherent name callback must not suppress safety admission.
	b.input = sqltype.InputEnvironment{TypeDiagnosticName: original.TypeDiagnosticName}
	captureEnumOperationSafety(op, &b.input)
	if err := e.captureEnumOperationInput(auth, &b.input); err != nil || b.input.EnumMemberSafe == nil || b.input.EnumMemberSafe(mood.ID(), members[1].ID) {
		t.Fatal("existing naming suppressed operation safety", err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		captureEnumOperationSafety(op, &b.input)
	}); allocations != 0 {
		t.Fatal("already admitted safety allocated again", allocations)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	raw := statement(t, "CREATE TABLE captured_default(v mood DEFAULT 'q')").(*parser.CreateTable)
	failure := e.schemaTableLocked(op, auth, stage, raw, domainUnitType(t, auth.catalog, "mood").namespace, auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	if d, ok := failure.(*sqlstate.Error); !ok || d.Code != "55P04" {
		t.Fatal("CREATE candidate binder lost admitted safety transport", failure)
	}
	mark := savepointMark{catalog: root}
	candidate := cloneCatalogChanges(root)
	candidate.enumUnsafe[members[0].ID] = true
	scope.owner.catalog = candidate
	b.input = sqltype.InputEnvironment{TypeDiagnosticName: original.TypeDiagnosticName}
	captureEnumOperationSafety(op, &b.input)
	if err := e.captureEnumOperationInput(auth, &b.input); err != nil {
		t.Fatal(err)
	}
	child := b.input.EnumMemberSafe
	if root.enumUnsafe[members[0].ID] || !original.EnumMemberSafe(mood.ID(), members[0].ID) || child(mood.ID(), members[0].ID) {
		t.Fatal("candidate mutation changed borrowed root or fresh capture missed it")
	}
	scope.owner.catalog = mark.catalog
	b.input = sqltype.InputEnvironment{TypeDiagnosticName: original.TypeDiagnosticName}
	captureEnumOperationSafety(op, &b.input)
	if err := e.captureEnumOperationInput(auth, &b.input); err != nil || !b.input.EnumMemberSafe(mood.ID(), members[0].ID) || b.input.EnumMemberSafe(mood.ID(), members[1].ID) || child(mood.ID(), members[0].ID) {
		t.Fatal("savepoint root replacement mutated old capture or failed refresh", err)
	}
	// Completion clears current provenance, not previously admitted captures.
	scope.owner.catalog = nil
	b.input = sqltype.InputEnvironment{}
	captureEnumOperationSafety(op, &b.input)
	if err := e.captureEnumOperationInput(auth, &b.input); err != nil || b.input.EnumMemberSafe != nil || original.EnumMemberSafe(mood.ID(), members[1].ID) || child(mood.ID(), members[0].ID) {
		t.Fatal("completed root retained stale safety or changed old capture", err)
	}
	for _, empty := range []*catalogChanges{nil, newCatalogChanges()} {
		scope.owner.catalog = empty
		probe := expressionBinder{}
		// Nil engine deliberately makes any unnecessary naming/path capture fail.
		if allocations := testing.AllocsPerRun(100, func() {
			captureEnumOperationSafety(op, &probe.input)
			if err := probe.engine.captureEnumOperationInput(auth, &probe.input); err != nil || probe.input.TypeDiagnosticName != nil || probe.input.EnumMemberSafe != nil {
				panic("empty provenance acquired an environment")
			}
		}); allocations != 0 {
			t.Fatal("empty provenance allocated", allocations)
		}
	}
	var absent sqltype.InputEnvironment
	captureEnumOperationSafety(nil, &absent)
	if absent.EnumMemberSafe != nil || absent.TypeDiagnosticName != nil {
		t.Fatal("absent operation acquired an environment")
	}
}
