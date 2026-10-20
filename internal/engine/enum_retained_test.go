package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
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
	if err := exec.EvaluationContext(c); err != nil {
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
	scope.owner.catalog.enumWritable().Unsafe = map[int64]bool{members[1].ID: true}
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enumSupportPlan, err := sqltype.BindEnumSupport("enum_last", mood.Usage(), mood.Usage())
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
			producer := functions.Enum{Info: expr.Info{Usage: mood.Usage()}, Plan: enumSupportPlan, Args: []expr.Expr{enumCapturedArgument{Info: expr.Info{Usage: mood.Usage()}, value: sqltype.Null(mood), calls: &calls}}}
			nested := plan.ConditionalExpr{Info: expr.Info{Usage: mood.Usage()}, Kind: parser.Coalesce, Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: mood.Usage()}, Value: safe}, producer}}
			e.mu.Lock()
			unsafeRoot := scope.owner.catalog
			scope.owner.catalog = newCatalogChanges()
			b := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth = &auth
			var defaults []expr.Expr
			var generations map[int32]catalog.ColumnGeneration
			var checks []catalog.CheckConstraint
			switch route {
			case "default":
				defaults = []expr.Expr{nested}
			case "generation":
				generations = map[int32]catalog.ColumnGeneration{1: {Planned: nested}}
			case "check":
				checks = []catalog.CheckConstraint{{Expression: nested}}
			}
			err := b.CaptureRetainedWriteInput(defaults, generations, checks)
			lazyNamingOnly := b.Input.TypeDiagnosticName != nil && b.Input.EnumMemberSafe == nil
			scope.owner.catalog = unsafeRoot
			if err == nil {
				captureEnumOperationSafety(op, &b.Input)
				err = e.captureEnumOperationInput(auth, &b.Input)
			}
			e.mu.Unlock()
			if err != nil || !lazyNamingOnly || calls != 0 || b.Input.TypeDiagnosticName == nil || b.Input.EnumMemberSafe == nil {
				t.Fatal("capture evaluated producer or lost environment", calls, err)
			}
			if b.Input.TypeDiagnosticName(mood.ID()) != "mood" || !b.Input.EnumMemberSafe(mood.ID(), members[0].ID) || b.Input.EnumMemberSafe(mood.ID(), members[1].ID) {
				t.Fatal("incoherent catalog/provenance capture")
			}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: b.Input}}
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
	root.enumWritable().Unsafe = map[int64]bool{members[1].ID: true}
	scope.owner.catalog = root
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	b := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	if err := e.captureEnumOperationInput(auth, &b.Input); err != nil {
		t.Fatal(err)
	}
	original := b.Input
	if original.TypeDiagnosticName == nil || original.TypeDiagnosticName(mood.ID()) != "mood" || original.EnumMemberSafe == nil || !original.EnumMemberSafe(mood.ID(), members[0].ID) || original.EnumMemberSafe(mood.ID(), members[1].ID) {
		t.Fatal("operation capture lost admitted naming or exact member safety")
	}
	// A preexisting coherent name callback must not suppress safety admission.
	b.Input = sqltype.InputEnvironment{TypeDiagnosticName: original.TypeDiagnosticName}
	captureEnumOperationSafety(op, &b.Input)
	if err := e.captureEnumOperationInput(auth, &b.Input); err != nil || b.Input.EnumMemberSafe == nil || b.Input.EnumMemberSafe(mood.ID(), members[1].ID) {
		t.Fatal("existing naming suppressed operation safety", err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		captureEnumOperationSafety(op, &b.Input)
	}); allocations != 0 {
		t.Fatal("already admitted safety allocated again", allocations)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	raw := statement(t, "CREATE TABLE captured_default(v mood DEFAULT 'q')").(*parser.CreateTable)
	failure := e.schemaTableLocked(op, auth, stage, raw, domainUnitType(t, auth.Catalog, "mood").ObjectNamespace, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	if d, ok := failure.(*sqlstate.Error); !ok || d.Code != "55P04" {
		t.Fatal("CREATE candidate binder lost admitted safety transport", failure)
	}
	mark := savepointMark{catalog: root}
	candidate := cloneCatalogChanges(root)
	candidate.enum.UnsafeMembers()[members[0].ID] = true
	scope.owner.catalog = candidate
	b.Input = sqltype.InputEnvironment{TypeDiagnosticName: original.TypeDiagnosticName}
	captureEnumOperationSafety(op, &b.Input)
	if err := e.captureEnumOperationInput(auth, &b.Input); err != nil {
		t.Fatal(err)
	}
	child := b.Input.EnumMemberSafe
	if root.enum.UnsafeMembers()[members[0].ID] || !original.EnumMemberSafe(mood.ID(), members[0].ID) || child(mood.ID(), members[0].ID) {
		t.Fatal("candidate mutation changed borrowed root or fresh capture missed it")
	}
	scope.owner.catalog = mark.catalog
	b.Input = sqltype.InputEnvironment{TypeDiagnosticName: original.TypeDiagnosticName}
	captureEnumOperationSafety(op, &b.Input)
	if err := e.captureEnumOperationInput(auth, &b.Input); err != nil || !b.Input.EnumMemberSafe(mood.ID(), members[0].ID) || b.Input.EnumMemberSafe(mood.ID(), members[1].ID) || child(mood.ID(), members[0].ID) {
		t.Fatal("savepoint root replacement mutated old capture or failed refresh", err)
	}
	// Completion clears current provenance, not previously admitted captures.
	scope.owner.catalog = nil
	b.Input = sqltype.InputEnvironment{}
	captureEnumOperationSafety(op, &b.Input)
	if err := e.captureEnumOperationInput(auth, &b.Input); err != nil || b.Input.EnumMemberSafe != nil || original.EnumMemberSafe(mood.ID(), members[1].ID) || child(mood.ID(), members[0].ID) {
		t.Fatal("completed root retained stale safety or changed old capture", err)
	}
	for _, empty := range []*catalogChanges{nil, newCatalogChanges()} {
		scope.owner.catalog = empty
		probe := analyze.Binder{}
		// Nil engine deliberately makes any unnecessary naming/path capture fail.
		if allocations := testing.AllocsPerRun(100, func() {
			captureEnumOperationSafety(op, &probe.Input)
			if err := (*Engine)(nil).captureEnumOperationInput(auth, &probe.Input); err != nil || probe.Input.TypeDiagnosticName != nil || probe.Input.EnumMemberSafe != nil {
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
