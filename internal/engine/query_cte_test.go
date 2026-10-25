package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cteUnitType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	typ, ok := New().CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 descriptor not found")
	}
	return typ
}
func cteUnitValue(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func cteUnitScan(expressions []expr.Expr, schema plan.QuerySchema, rows int) (*exec.CTEInvocation, *exec.State, *plan.DerivedNode) {
	project := &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{Rows: make([][]expr.Expr, rows)}, Expressions: expressions, Frame: 2}
	definition := &plan.CTEDefinition{Query: &plan.Query{Root: project}, Owner: 1, References: 2, Hint: parser.CTEMaterialized}
	state := &exec.State{Source: &exec.Source{Materialized: true}}
	owner := &exec.CTEInvocation{Frame: 1, State: *state}
	state.Ctes = owner
	return owner, state, &plan.DerivedNode{QuerySchema: schema, Query: definition.Query, CTE: definition}
}
func cteUnitNext(t *testing.T, stage *exec.Stage) []sqltype.Value {
	t.Helper()
	index, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal(index, present, err)
	}
	return stage.Source.Row(stage.Index(index))
}

func TestQueryCTESharedFrontierAndReaderLifetime(t *testing.T) {
	typ := cteUnitType(t)
	draws := 0
	expression := queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) { draws++; return sqltype.Integer(typ, int64(draws)) })
	owner, state, node := cteUnitScan([]expr.Expr{expression}, plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}, 3)
	first, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	second, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if draws != 0 || owner.Producers[node.CTE].Opened {
		t.Fatal("opened producer without demand")
	}
	firstRow := cteUnitNext(t, &first)
	if draws != 1 {
		t.Fatal(draws)
	}
	if row := cteUnitNext(t, &second); draws != 1 || row[0] != firstRow[0] {
		t.Fatal(draws, row, firstRow)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &second)
	if draws != 2 {
		t.Fatal(draws)
	}
	third, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &third)
	cteUnitNext(t, &third)
	if draws != 2 {
		t.Fatal("replay redrew", draws)
	}
	cteUnitNext(t, &third)
	if draws != 3 {
		t.Fatal(draws)
	}
	if _, present, err := third.NextOccurrence(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	cteUnitNext(t, &second)
	if draws != 3 {
		t.Fatal(draws)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if node.CTE.Query.Root != node.Query.Root || node.CTE.References != 2 {
		t.Fatal("runtime mutated immutable definition")
	}
}

func TestQueryCTEZeroWidthAndNullRows(t *testing.T) {
	for _, width := range []int{0, 1} {
		t.Run(map[int]string{0: "zero width", 1: "NULL"}[width], func(t *testing.T) {
			var expressions []expr.Expr
			var schema plan.QuerySchema
			if width == 1 {
				typ := cteUnitType(t)
				expressions = []expr.Expr{queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Null(typ), nil })}
				schema = plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}
			}
			owner, state, node := cteUnitScan(expressions, schema, 2)
			stage, err := exec.OpenCTEScan(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				row := cteUnitNext(t, &stage)
				if len(row) != width || width == 1 && !row[0].IsNull() {
					t.Fatal(row)
				}
			}
			if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
				t.Fatal(present, err)
			}
			producer := owner.Producers[node.CTE]
			if producer.Source.FlatCount != 2 || len(producer.Source.Flat) != 2*width {
				t.Fatal(producer.Source.FlatCount, producer.Source.Flat)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueryCTEStickyFailureAndCloseOnce(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "expression", true: "cancel replay"}[cancelled], func(t *testing.T) {
			typ := cteUnitType(t)
			primary := errors.New("original producer failure")
			cleanup := errors.New("cleanup failure")
			draws := 0
			expression := queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) {
				draws++
				if draws == 2 {
					return sqltype.Value{}, primary
				}
				return sqltype.Integer(typ, 1)
			})
			owner, state, node := cteUnitScan([]expr.Expr{expression}, plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}, 3)
			stage, err := exec.OpenCTEScan(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			cteUnitNext(t, &stage)
			producer := owner.Producers[node.CTE]
			closes := 0
			producer.Inner.Finish = func() error { closes++; return cleanup }
			ctx := context.Background()
			index := 1
			if cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				primary = context.Canceled
				index = 0
			}
			if _, err := producer.Ensure(ctx, index); err != primary {
				t.Fatal(err, primary)
			}
			if _, err := producer.Ensure(context.Background(), 0); err != primary {
				t.Fatal("cached prefix escaped sticky failure", err)
			}
			if err := owner.Close(); err != primary {
				t.Fatal(err)
			}
			if err := owner.Close(); err != primary || closes != 1 {
				t.Fatal(err, closes)
			}
		})
	}
}

func TestQueryCTEInvocationIsolationAndInheritance(t *testing.T) {
	typ := cteUnitType(t)
	draws := 0
	probe := queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) { draws++; return sqltype.Integer(typ, int64(draws)) })
	outer, state, outerNode := cteUnitScan([]expr.Expr{probe}, plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}, 1)
	first, err := exec.OpenCTEScan(context.Background(), state, outerNode)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &first)
	for _, value := range []int64{11, 22} {
		correlated := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 7, Slot: 0}
		localProject := &plan.ProjectNode{QuerySchema: outerNode.Schema(), Child: &plan.SingletonNode{}, Expressions: []expr.Expr{correlated}, Frame: 3}
		localDefinition := &plan.CTEDefinition{Query: &plan.Query{Root: localProject}, Owner: 2, Hint: parser.CTEMaterialized}
		localState := *state
		localState.Outer = &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 7, Row: []sqltype.Value{cteUnitValue(t, typ, value)}}}
		local := &exec.CTEInvocation{Frame: 2, Parent: outer, State: localState}
		localState.Ctes = local
		inherited, err := exec.OpenCTEScan(context.Background(), &localState, outerNode)
		if err != nil {
			t.Fatal(err)
		}
		cteUnitNext(t, &inherited)
		if draws != 1 {
			t.Fatal("enclosing cache not inherited", draws)
		}
		localNode := &plan.DerivedNode{QuerySchema: outerNode.Schema(), Query: localDefinition.Query, CTE: localDefinition}
		stage, err := exec.OpenCTEScan(context.Background(), &localState, localNode)
		if err != nil {
			t.Fatal(err)
		}
		if row := cteUnitNext(t, &stage); row[0] != cteUnitValue(t, typ, value) {
			t.Fatal(row, value)
		}
		if err := local.Close(); err != nil {
			t.Fatal(err)
		}
	}
	fresh := &exec.CTEInvocation{Frame: 1, State: outer.State}
	freshState := outer.State
	freshState.Ctes = fresh
	stage, err := exec.OpenCTEScan(context.Background(), &freshState, outerNode)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	if draws != 2 {
		t.Fatal("fresh invocation reused cache", draws)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	if err := outer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryCTEWholeDefinitionVolatility(t *testing.T) {
	typ := cteUnitType(t)
	volatile := queryProbeExpression{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, eval: func(*exec.Context) (sqltype.Value, error) {
		t.Fatal("analysis evaluated target")
		return sqltype.Value{}, nil
	}}
	body := &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{volatile}, Frame: 2}
	wrapper := &plan.ProjectNode{Child: &plan.SingletonNode{}, Frame: 1, AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{QueryPlan: &plan.Query{Root: body}, CTEEdge: true}}}
	if !analyze.CTEVolatile(wrapper) {
		t.Fatal("static eligibility omitted unused nested definition")
	}
	if analyze.QueryDemandProperties(wrapper, false).Volatility == expr.Volatile {
		t.Fatal("unused definition became executable demand")
	}
	wrapper.Expressions = []expr.Expr{plan.SubqueryExpr{QueryPlan: &plan.Query{Root: body}}}
	if !analyze.CTEVolatile(wrapper) {
		t.Fatal("nested projected volatility omitted")
	}
	wrapper.Expressions = []expr.Expr{volatile}
	if !analyze.CTEVolatile(wrapper) {
		t.Fatal("unused projected volatility omitted")
	}
	for _, hint := range []parser.CTEMaterialization{parser.CTEDefault, parser.CTEMaterialized, parser.CTENotMaterialized} {
		for _, references := range []int{1, 2} {
			definition := plan.CTEDefinition{Hint: hint, References: references}
			want := hint != parser.CTEMaterialized && (hint == parser.CTENotMaterialized || references == 1)
			if definition.Folded() != want {
				t.Fatal(hint, references)
			}
			definition.Volatile = true
			if definition.Folded() {
				t.Fatal("volatile definition folded", hint, references)
			}
		}
	}
}

func TestQueryCTEStoredIndependentGraphMemo(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	truth := sqltype.Boolean(boolean, true)
	falsity := sqltype.Boolean(boolean, false)
	literal := func(value sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	}
	canonical := &plan.ProjectNode{Child: &plan.SingletonNode{}, Frame: 2, Expressions: []expr.Expr{literal(cteUnitValue(t, typ, 1))}, QuerySchema: plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}}
	left, right := *canonical, *canonical
	left.Child = &plan.FilterNode{Child: canonical.Child, Predicate: literal(truth)}
	right.Child = &plan.FilterNode{Child: canonical.Child, Predicate: literal(falsity)}
	definition := &plan.CTEDefinition{Query: &plan.Query{Root: canonical}, Owner: 1, References: 2, Hint: parser.CTENotMaterialized}
	leftScan := &plan.DerivedNode{QuerySchema: canonical.Schema(), Query: &plan.Query{Root: &left}, CTE: definition}
	rightScan := &plan.DerivedNode{QuerySchema: canonical.Schema(), Query: &plan.Query{Root: &right}, CTE: definition}
	remapper := &stored.ViewQueryRemapper{Catalog: e.CatalogRead(), Ref: func(ref ids.ObjectRef) ids.ObjectRef { return ref }, Type: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }}
	mappedLeft, err := remapper.Query(leftScan)
	if err != nil {
		t.Fatal(err)
	}
	mappedRight, err := remapper.Query(rightScan)
	if err != nil {
		t.Fatal(err)
	}
	l, r := mappedLeft.(*plan.DerivedNode), mappedRight.(*plan.DerivedNode)
	if l.Query.Root == r.Query.Root || l.CTE != r.CTE || l.CTE.Query.Root == l.Query.Root || l.CTE.Query.Root == r.Query.Root {
		t.Fatal("occurrence predicates collapsed into canonical body")
	}
	first, err := remapper.Nested(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, err := remapper.Nested(canonical)
	if err != nil || first != again || first != l.CTE.Query.Root {
		t.Fatal("canonical root sharing lost", err)
	}
	if definition.Query.Root != canonical || left.Child.(*plan.FilterNode).Predicate != literal(truth) || right.Child.(*plan.FilterNode).Predicate != literal(falsity) {
		t.Fatal("original graph mutated")
	}
}

func TestQueryCTEFoldPlacementFrameAndImmutability(t *testing.T) {
	e := New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	inputSchema := plan.QuerySchema{{Name: "keep", Usage: boolean.Usage()}}
	literal := func(value bool) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, value)}
	}
	input := &plan.ValuesNode{QuerySchema: inputSchema, Rows: [][]expr.Expr{{literal(false)}, {literal(true)}}}
	local := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage()}, Scope: 1, Frame: 2, Slot: 0}
	draws := 0
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "keep", Usage: boolean.Usage()}, {Name: "n", Usage: integer.Usage()}}, Child: input, Expressions: []expr.Expr{local, queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { draws++; return sqltype.Integer(integer, 42) })}, Frame: 2}
	definition := &plan.CTEDefinition{Query: &plan.Query{Root: project}, Owner: 1, References: 2, Hint: parser.CTENotMaterialized}
	scan := &plan.DerivedNode{QuerySchema: project.Schema(), Query: definition.Query, CTE: definition, ScanFrame: 1, Demand: []bool{false, true}}
	parent := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	mapped, moved, err := analyze.PushCTEOccurrence(e.AnalysisHost(nil), e.CatalogRead(), scan, parent, 0)
	if err != nil || !moved {
		t.Fatal(moved, err)
	}
	if project.Child != input || scan.Query.Root != project || mapped == scan {
		t.Fatal("fold mutated original")
	}
	stage, err := exec.OpenDerivedNode(context.Background(), &exec.State{Source: &exec.Source{Materialized: true}}, mapped.(*plan.DerivedNode))
	if err != nil {
		t.Fatal(err)
	}
	row := cteUnitNext(t, &stage)
	if draws != 1 || row[1] != cteUnitValue(t, integer, 42) {
		t.Fatal(draws, row)
	}
	if _, present, err := stage.NextOccurrence(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	wrong := parent
	wrong.Frame = 99
	if _, moved, err := analyze.PushCTEOccurrence(e.AnalysisHost(nil), e.CatalogRead(), scan, wrong, 0); err != nil || moved {
		t.Fatal("foreign frame restriction moved", moved, err)
	}
	wrong = parent
	wrong.Slot = 2
	if _, moved, err := analyze.PushCTEOccurrence(e.AnalysisHost(nil), e.CatalogRead(), scan, wrong, 0); err != nil || moved {
		t.Fatal("other occurrence restriction moved", moved, err)
	}
}

func TestQueryCTEProjectDispatcherOwnsInvocation(t *testing.T) {
	typ := cteUnitType(t)
	draws := 0
	expression := queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) {
		draws++
		return sqltype.Integer(typ, int64(draws))
	})
	_, state, node := cteUnitScan([]expr.Expr{expression}, plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}, 2)
	state.Ctes = nil
	original := node.Query.Root
	root := &plan.ProjectNode{
		QuerySchema: node.Schema(), Child: node, Frame: 1, CTEOwner: true,
		Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 0}},
	}
	stage, err := exec.OpenNode(context.Background(), state, root)
	if err != nil {
		t.Fatal(err)
	}
	owner := state.Ctes
	if owner == nil || owner.Frame != 1 || owner.Closed || draws != 0 {
		t.Fatal("direct project dispatch did not install a lazy owner", owner, draws)
	}
	cteUnitNext(t, &stage)
	reader, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &reader)
	if draws != 1 {
		t.Fatal("reader did not inherit direct-dispatch producer", draws)
	}
	producer := owner.Producers[node.CTE]
	closes := 0
	finish := producer.Inner.Finish
	producer.Inner.Finish = func() error {
		closes++
		if finish != nil {
			return finish()
		}
		return nil
	}
	if err := reader.Close(); err != nil || closes != 0 {
		t.Fatal("reader closed owning producer", err, closes)
	}
	if err := stage.Close(); err != nil || !owner.Closed || closes != 1 {
		t.Fatal("project did not close invocation once", err, closes)
	}
	if node.Query.Root != original || root.Child != node {
		t.Fatal("dispatch mutated immutable plan")
	}
}

func TestQueryCTEFilterDemandGate(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	integer := cteUnitType(t)
	info := expr.Info{Usage: boolean.Usage()}
	for _, tc := range []struct {
		name               string
		predicate          expr.Expr
		parameters         []sqltype.Value
		wantRow, wantError bool
	}{
		{"false", plan.LiteralExpr{Info: info, Value: sqltype.Boolean(boolean, false)}, nil, false, false},
		{"null", plan.LiteralExpr{Info: info, Value: sqltype.Null(boolean)}, nil, false, false},
		{"bound false", plan.ParameterExpr{Info: info, Number: 1}, []sqltype.Value{sqltype.Boolean(boolean, false)}, false, false},
		{"bound null", plan.ParameterExpr{Info: info, Number: 1}, []sqltype.Value{sqltype.Null(boolean)}, false, false},
		{"bound true", plan.ParameterExpr{Info: info, Number: 1}, []sqltype.Value{sqltype.Boolean(boolean, true)}, true, false},
		{"missing parameter", plan.ParameterExpr{Info: info, Number: 1}, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draws := 0
			expression := queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { draws++; return sqltype.Integer(integer, 1) })
			owner, state, child := cteUnitScan([]expr.Expr{expression}, plan.QuerySchema{{Name: "n", Usage: integer.Usage()}}, 1)
			defer owner.Close()
			state.Parameters = tc.parameters
			node := &plan.FilterNode{QuerySchema: child.Schema(), Child: child, Predicate: tc.predicate}
			stage, err := exec.OpenNode(context.Background(), state, node)
			if tc.wantError {
				if err == nil || draws != 0 || len(owner.Producers) != 0 {
					t.Fatal("parameter failure opened child", err, draws, len(owner.Producers))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if len(stage.Columns) != 1 || stage.Columns[0].Name != "n" || stage.Columns[0].Usage.Type != integer {
				t.Fatal("empty gate lost analyzed schema", stage.Columns)
			}
			_, present, err := stage.NextOccurrence(context.Background())
			if err != nil || present != tc.wantRow {
				t.Fatal(present, err)
			}
			if tc.wantRow {
				if draws != 1 || len(owner.Producers) != 1 {
					t.Fatal("true gate did not demand child", draws, len(owner.Producers))
				}
			} else if draws != 0 || len(owner.Producers) != 0 {
				t.Fatal("dead gate opened or pulled producer", draws, len(owner.Producers))
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := exec.OpenNode(cancelled, state, node); !errors.Is(err, context.Canceled) {
				t.Fatal("dead gate suppressed cancellation", err)
			}
		})
	}
}

func TestQueryCTEFilterDoesNotPreEvaluatePredicate(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	integer := cteUnitType(t)
	draws, evaluations := 0, 0
	expression := queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { draws++; return sqltype.Integer(integer, 1) })
	owner, state, child := cteUnitScan([]expr.Expr{expression}, plan.QuerySchema{{Name: "n", Usage: integer.Usage()}}, 1)
	defer owner.Close()
	predicate := queryProbe(boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
		evaluations++
		if draws == 0 {
			return sqltype.Value{}, errors.New("predicate evaluated before child demand")
		}
		return sqltype.Boolean(boolean, false), nil
	})
	stage, err := exec.OpenNode(context.Background(), state, &plan.FilterNode{QuerySchema: child.Schema(), Child: child, Predicate: predicate})
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if draws != 0 || evaluations != 0 {
		t.Fatal("opening eagerly evaluated arbitrary expressions", draws, evaluations)
	}
	if _, present, err := stage.NextOccurrence(context.Background()); err != nil || present || draws != 1 || evaluations != 1 {
		t.Fatal("ordinary predicate demand changed", present, err, draws, evaluations)
	}
}

func TestQueryCTEDeadZeroWidthOwner(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	_, state, child := cteUnitScan(nil, plan.QuerySchema{}, 2)
	state.Ctes = nil
	state.Source.Locks = &exec.LockContext{Leaves: []exec.QueryLockLeaf{{Mark: plan.RowMark{Mode: lock.ForUpdate}}}}
	filter := &plan.FilterNode{QuerySchema: child.Schema(), Child: child, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}
	root := &plan.ProjectNode{QuerySchema: child.Schema(), Child: filter, Frame: 1, CTEOwner: true}
	stage, err := exec.OpenNode(context.Background(), state, root)
	if err != nil {
		t.Fatal(err)
	}
	owner := state.Ctes
	if owner == nil || len(stage.Columns) != 0 || len(owner.Producers) != 0 {
		t.Fatal("zero-width gate lost owner/schema or opened producer", owner, stage.Columns)
	}
	if _, present, err := stage.NextOccurrence(context.Background()); err != nil || present {
		t.Fatal("zero-width dead consumer retained occurrences", present, err)
	}
	if err := stage.Close(); err != nil || !owner.Closed || len(owner.Producers) != 0 {
		t.Fatal("dead consumer did not close empty owner", owner, err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal("repeated empty owner close", err)
	}
}
