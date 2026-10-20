package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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
func cteUnitScan(expressions []expr.Expr, schema querySchema, rows int) (*cteInvocation, *queryState, *derivedNode) {
	project := &projectNode{querySchema: schema, Child: &valuesNode{Rows: make([][]expr.Expr, rows)}, Expressions: expressions, Frame: 2}
	definition := &cteDefinition{Query: &BoundQuery{Root: project}, Owner: 1, References: 2, Hint: parser.CTEMaterialized}
	state := &queryState{source: &querySource{materialized: true}}
	owner := &cteInvocation{frame: 1, state: *state}
	state.ctes = owner
	return owner, state, &derivedNode{querySchema: schema, Query: definition.Query, CTE: definition}
}
func cteUnitNext(t *testing.T, stage *indexedStage) []sqltype.Value {
	t.Helper()
	index, present, err := stage.nextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal(index, present, err)
	}
	return stage.source.row(stage.index(index))
}

func TestQueryCTESharedFrontierAndReaderLifetime(t *testing.T) {
	typ := cteUnitType(t)
	draws := 0
	expression := queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.Integer(typ, int64(draws)) })
	owner, state, node := cteUnitScan([]expr.Expr{expression}, querySchema{{Name: "n", Usage: typ.Usage()}}, 3)
	first, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if draws != 0 || owner.producers[node.CTE].opened {
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
	third, err := openCTEScan(context.Background(), state, node)
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
	if _, present, err := third.nextOccurrence(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	cteUnitNext(t, &second)
	if draws != 3 {
		t.Fatal(draws)
	}
	if err := owner.close(); err != nil {
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
			var schema querySchema
			if width == 1 {
				typ := cteUnitType(t)
				expressions = []expr.Expr{queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Null(typ), nil })}
				schema = querySchema{{Name: "n", Usage: typ.Usage()}}
			}
			owner, state, node := cteUnitScan(expressions, schema, 2)
			stage, err := openCTEScan(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				row := cteUnitNext(t, &stage)
				if len(row) != width || width == 1 && !row[0].IsNull() {
					t.Fatal(row)
				}
			}
			if _, present, err := stage.nextOccurrence(context.Background()); present || err != nil {
				t.Fatal(present, err)
			}
			producer := owner.producers[node.CTE]
			if producer.source.flatCount != 2 || len(producer.source.flat) != 2*width {
				t.Fatal(producer.source.flatCount, producer.source.flat)
			}
			if err := owner.close(); err != nil {
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
			expression := queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) {
				draws++
				if draws == 2 {
					return sqltype.Value{}, primary
				}
				return sqltype.Integer(typ, 1)
			})
			owner, state, node := cteUnitScan([]expr.Expr{expression}, querySchema{{Name: "n", Usage: typ.Usage()}}, 3)
			stage, err := openCTEScan(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			cteUnitNext(t, &stage)
			producer := owner.producers[node.CTE]
			closes := 0
			producer.inner.finish = func() error { closes++; return cleanup }
			ctx := context.Background()
			index := 1
			if cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				primary = context.Canceled
				index = 0
			}
			if _, err := producer.ensure(ctx, index); err != primary {
				t.Fatal(err, primary)
			}
			if _, err := producer.ensure(context.Background(), 0); err != primary {
				t.Fatal("cached prefix escaped sticky failure", err)
			}
			if err := owner.close(); err != primary {
				t.Fatal(err)
			}
			if err := owner.close(); err != primary || closes != 1 {
				t.Fatal(err, closes)
			}
		})
	}
}

func TestQueryCTESuccessfulCloseFailureVeto(t *testing.T) {
	owner, state, node := cteUnitScan(nil, nil, 1)
	stage, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	producer := owner.producers[node.CTE]
	failure := errors.New("required close failed")
	closes := 0
	producer.inner.finish = func() error { closes++; return failure }
	if _, err := producer.ensure(context.Background(), 1); err != failure {
		t.Fatal(err)
	}
	if _, err := producer.ensure(context.Background(), 0); err != failure {
		t.Fatal(err)
	}
	if err := owner.close(); err != failure || closes != 1 {
		t.Fatal(err, closes)
	}
}

func TestQueryCTEInvocationIsolationAndInheritance(t *testing.T) {
	typ := cteUnitType(t)
	draws := 0
	probe := queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.Integer(typ, int64(draws)) })
	outer, state, outerNode := cteUnitScan([]expr.Expr{probe}, querySchema{{Name: "n", Usage: typ.Usage()}}, 1)
	first, err := openCTEScan(context.Background(), state, outerNode)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &first)
	for _, value := range []int64{11, 22} {
		correlated := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 7, Slot: 0}
		localProject := &projectNode{querySchema: outerNode.Schema(), Child: &singletonNode{}, Expressions: []expr.Expr{correlated}, Frame: 3}
		localDefinition := &cteDefinition{Query: &BoundQuery{Root: localProject}, Owner: 2, Hint: parser.CTEMaterialized}
		localState := *state
		localState.outer = &EvalContext{evalFrame: evalFrame{Scope: 1, Frame: 7, Row: []sqltype.Value{cteUnitValue(t, typ, value)}}}
		local := &cteInvocation{frame: 2, parent: outer, state: localState}
		localState.ctes = local
		inherited, err := openCTEScan(context.Background(), &localState, outerNode)
		if err != nil {
			t.Fatal(err)
		}
		cteUnitNext(t, &inherited)
		if draws != 1 {
			t.Fatal("enclosing cache not inherited", draws)
		}
		localNode := &derivedNode{querySchema: outerNode.Schema(), Query: localDefinition.Query, CTE: localDefinition}
		stage, err := openCTEScan(context.Background(), &localState, localNode)
		if err != nil {
			t.Fatal(err)
		}
		if row := cteUnitNext(t, &stage); row[0] != cteUnitValue(t, typ, value) {
			t.Fatal(row, value)
		}
		if err := local.close(); err != nil {
			t.Fatal(err)
		}
	}
	fresh := &cteInvocation{frame: 1, state: outer.state}
	freshState := outer.state
	freshState.ctes = fresh
	stage, err := openCTEScan(context.Background(), &freshState, outerNode)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	if draws != 2 {
		t.Fatal("fresh invocation reused cache", draws)
	}
	if err := fresh.close(); err != nil {
		t.Fatal(err)
	}
	if err := outer.close(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryCTEWholeDefinitionVolatility(t *testing.T) {
	typ := cteUnitType(t)
	volatile := queryProbeExpression{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, eval: func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("analysis evaluated target")
		return sqltype.Value{}, nil
	}}
	body := &projectNode{Child: &singletonNode{}, Expressions: []expr.Expr{volatile}, Frame: 2}
	wrapper := &projectNode{Child: &singletonNode{}, Frame: 1, AnalysisExpressions: []expr.Expr{subqueryExpression{QueryPlan: &BoundQuery{Root: body}, cteDefinition: true}}}
	if !cteVolatile(wrapper) {
		t.Fatal("static eligibility omitted unused nested definition")
	}
	if queryDemandProperties(wrapper, false).Volatility == expr.Volatile {
		t.Fatal("unused definition became executable demand")
	}
	wrapper.Expressions = []expr.Expr{subqueryExpression{QueryPlan: &BoundQuery{Root: body}}}
	if !cteVolatile(wrapper) {
		t.Fatal("nested projected volatility omitted")
	}
	wrapper.Expressions = []expr.Expr{volatile}
	if !cteVolatile(wrapper) {
		t.Fatal("unused projected volatility omitted")
	}
	for _, hint := range []parser.CTEMaterialization{parser.CTEDefault, parser.CTEMaterialized, parser.CTENotMaterialized} {
		for _, references := range []int{1, 2} {
			definition := cteDefinition{Hint: hint, References: references}
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

func TestQueryCTEOnlyActiveMarksFenceFolding(t *testing.T) {
	root := &projectNode{Child: &rowLockNode{Child: &singletonNode{}, Marks: []queryRowMark{{}}}}
	if cteVolatile(root) {
		t.Fatal("ignored virtual row mark fenced folding")
	}
	root.Child.(*rowLockNode).Marks[0].Mode = queryRowMode("FOR UPDATE")
	if !cteVolatile(root) {
		t.Fatal("active physical row mark omitted")
	}
}

func TestQueryCTEStoredIndependentGraphMemo(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	truth := sqltype.Boolean(boolean, true)
	falsity := sqltype.Boolean(boolean, false)
	literal := func(value sqltype.Value) expr.Expr {
		return literalExpression{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	}
	canonical := &projectNode{Child: &singletonNode{}, Frame: 2, Expressions: []expr.Expr{literal(cteUnitValue(t, typ, 1))}, querySchema: querySchema{{Name: "n", Usage: typ.Usage()}}}
	left, right := *canonical, *canonical
	left.Child = &filterNode{Child: canonical.Child, Predicate: literal(truth)}
	right.Child = &filterNode{Child: canonical.Child, Predicate: literal(falsity)}
	definition := &cteDefinition{Query: &BoundQuery{Root: canonical}, Owner: 1, References: 2, Hint: parser.CTENotMaterialized}
	leftScan := &derivedNode{querySchema: canonical.Schema(), Query: &BoundQuery{Root: &left}, CTE: definition}
	rightScan := &derivedNode{querySchema: canonical.Schema(), Query: &BoundQuery{Root: &right}, CTE: definition}
	remapper := &viewQueryRemapper{catalog: e.CatalogRead(), ref: func(ref ids.ObjectRef) ids.ObjectRef { return ref }, typ: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }}
	mappedLeft, err := remapper.query(leftScan)
	if err != nil {
		t.Fatal(err)
	}
	mappedRight, err := remapper.query(rightScan)
	if err != nil {
		t.Fatal(err)
	}
	l, r := mappedLeft.(*derivedNode), mappedRight.(*derivedNode)
	if l.Query.Root == r.Query.Root || l.CTE != r.CTE || l.CTE.Query.Root == l.Query.Root || l.CTE.Query.Root == r.Query.Root {
		t.Fatal("occurrence predicates collapsed into canonical body")
	}
	first, err := remapper.nested(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, err := remapper.nested(canonical)
	if err != nil || first != again || first != l.CTE.Query.Root {
		t.Fatal("canonical root sharing lost", err)
	}
	if definition.Query.Root != canonical || left.Child.(*filterNode).Predicate != literal(truth) || right.Child.(*filterNode).Predicate != literal(falsity) {
		t.Fatal("original graph mutated")
	}
}

func TestQueryCTEFoldPlacementFrameAndImmutability(t *testing.T) {
	e := New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	inputSchema := querySchema{{Name: "keep", Usage: boolean.Usage()}}
	literal := func(value bool) expr.Expr {
		return literalExpression{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, value)}
	}
	input := &valuesNode{querySchema: inputSchema, Rows: [][]expr.Expr{{literal(false)}, {literal(true)}}}
	local := columnExpression{Info: expr.Info{Usage: boolean.Usage()}, Scope: 1, Frame: 2, Slot: 0}
	draws := 0
	project := &projectNode{querySchema: querySchema{{Name: "keep", Usage: boolean.Usage()}, {Name: "n", Usage: integer.Usage()}}, Child: input, Expressions: []expr.Expr{local, queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.Integer(integer, 42) })}, Frame: 2}
	definition := &cteDefinition{Query: &BoundQuery{Root: project}, Owner: 1, References: 2, Hint: parser.CTENotMaterialized}
	scan := &derivedNode{querySchema: project.Schema(), Query: definition.Query, CTE: definition, ScanFrame: 1, Demand: []bool{false, true}}
	parent := columnExpression{Info: expr.Info{Usage: boolean.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	mapped, moved, err := pushCTEOccurrence(e.CatalogRead(), scan, parent, 0)
	if err != nil || !moved {
		t.Fatal(moved, err)
	}
	if project.Child != input || scan.Query.Root != project || mapped == scan {
		t.Fatal("fold mutated original")
	}
	stage, err := openDerivedNode(context.Background(), &queryState{source: &querySource{materialized: true}}, mapped.(*derivedNode))
	if err != nil {
		t.Fatal(err)
	}
	row := cteUnitNext(t, &stage)
	if draws != 1 || row[1] != cteUnitValue(t, integer, 42) {
		t.Fatal(draws, row)
	}
	if _, present, err := stage.nextOccurrence(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	wrong := parent
	wrong.Frame = 99
	if _, moved, err := pushCTEOccurrence(e.CatalogRead(), scan, wrong, 0); err != nil || moved {
		t.Fatal("foreign frame restriction moved", moved, err)
	}
	wrong = parent
	wrong.Slot = 2
	if _, moved, err := pushCTEOccurrence(e.CatalogRead(), scan, wrong, 0); err != nil || moved {
		t.Fatal("other occurrence restriction moved", moved, err)
	}
}

func TestQueryCTEProjectDispatcherOwnsInvocation(t *testing.T) {
	typ := cteUnitType(t)
	draws := 0
	expression := queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) {
		draws++
		return sqltype.Integer(typ, int64(draws))
	})
	_, state, node := cteUnitScan([]expr.Expr{expression}, querySchema{{Name: "n", Usage: typ.Usage()}}, 2)
	state.ctes = nil
	original := node.Query.Root
	root := &projectNode{
		querySchema: node.Schema(), Child: node, Frame: 1, CTEOwner: true,
		Expressions: []expr.Expr{columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 0}},
	}
	stage, err := openQueryNode(context.Background(), state, root)
	if err != nil {
		t.Fatal(err)
	}
	owner := state.ctes
	if owner == nil || owner.frame != 1 || owner.closed || draws != 0 {
		t.Fatal("direct project dispatch did not install a lazy owner", owner, draws)
	}
	cteUnitNext(t, &stage)
	reader, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &reader)
	if draws != 1 {
		t.Fatal("reader did not inherit direct-dispatch producer", draws)
	}
	producer := owner.producers[node.CTE]
	closes := 0
	finish := producer.inner.finish
	producer.inner.finish = func() error {
		closes++
		if finish != nil {
			return finish()
		}
		return nil
	}
	if err := reader.Close(); err != nil || closes != 0 {
		t.Fatal("reader closed owning producer", err, closes)
	}
	if err := stage.Close(); err != nil || !owner.closed || closes != 1 {
		t.Fatal("project did not close invocation once", err, closes)
	}
	if node.Query.Root != original || root.Child != node {
		t.Fatal("dispatch mutated immutable plan")
	}
}

func TestQueryCTEExplicitLockTargetKind(t *testing.T) {
	virtual := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "c"}}}}
	physical := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}}
	from := &parser.JoinReference{
		Left:  &parser.AliasReference{Input: virtual, Name: parser.Identifier{Text: "z"}},
		Right: physical,
	}
	classification := map[*parser.TableReference]bool{virtual: true}
	for _, mode := range []string{"FOR UPDATE", "FOR NO KEY UPDATE", "FOR SHARE", "FOR KEY SHARE"} {
		target := parser.ObjectName{Parts: []parser.Identifier{{Text: "z", Pos: parser.Position{Character: 73}}}}
		_, err := queryLockTargets(from, []parser.RowLockClause{{Mode: mode, Targets: []parser.ObjectName{target}}}, classification)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != mode+" cannot be applied to a WITH query" || diagnostic.Position != "73" {
			t.Fatal(mode, err)
		}
		marks, err := queryLockTargets(from, []parser.RowLockClause{{Mode: mode}}, classification)
		if err != nil || len(marks) != 2 || marks[0].Mode != 0 || marks[1].Mode != queryRowMode(mode) {
			t.Fatal("untargeted virtual entry affected physical marks", mode, marks, err)
		}
		target.Parts[0].Text = "t"
		marks, err = queryLockTargets(from, []parser.RowLockClause{{Mode: mode, Targets: []parser.ObjectName{target}}}, classification)
		if err != nil || marks[0].Mode != 0 || marks[1].Mode != queryRowMode(mode) {
			t.Fatal("physical target lost", mode, marks, err)
		}
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
		{"false", literalExpression{Info: info, Value: sqltype.Boolean(boolean, false)}, nil, false, false},
		{"null", literalExpression{Info: info, Value: sqltype.Null(boolean)}, nil, false, false},
		{"bound false", parameterExpression{Info: info, Number: 1}, []sqltype.Value{sqltype.Boolean(boolean, false)}, false, false},
		{"bound null", parameterExpression{Info: info, Number: 1}, []sqltype.Value{sqltype.Null(boolean)}, false, false},
		{"bound true", parameterExpression{Info: info, Number: 1}, []sqltype.Value{sqltype.Boolean(boolean, true)}, true, false},
		{"missing parameter", parameterExpression{Info: info, Number: 1}, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draws := 0
			expression := queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.Integer(integer, 1) })
			owner, state, child := cteUnitScan([]expr.Expr{expression}, querySchema{{Name: "n", Usage: integer.Usage()}}, 1)
			defer owner.close()
			state.parameters = tc.parameters
			node := &filterNode{querySchema: child.Schema(), Child: child, Predicate: tc.predicate}
			stage, err := openQueryNode(context.Background(), state, node)
			if tc.wantError {
				if err == nil || draws != 0 || len(owner.producers) != 0 {
					t.Fatal("parameter failure opened child", err, draws, len(owner.producers))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if len(stage.schema) != 1 || stage.schema[0].Name != "n" || stage.schema[0].Usage.Type != integer {
				t.Fatal("empty gate lost analyzed schema", stage.schema)
			}
			_, present, err := stage.nextOccurrence(context.Background())
			if err != nil || present != tc.wantRow {
				t.Fatal(present, err)
			}
			if tc.wantRow {
				if draws != 1 || len(owner.producers) != 1 {
					t.Fatal("true gate did not demand child", draws, len(owner.producers))
				}
			} else if draws != 0 || len(owner.producers) != 0 {
				t.Fatal("dead gate opened or pulled producer", draws, len(owner.producers))
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := openQueryNode(cancelled, state, node); !errors.Is(err, context.Canceled) {
				t.Fatal("dead gate suppressed cancellation", err)
			}
		})
	}
}

func TestQueryCTEFilterDoesNotPreEvaluatePredicate(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	integer := cteUnitType(t)
	draws, evaluations := 0, 0
	expression := queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.Integer(integer, 1) })
	owner, state, child := cteUnitScan([]expr.Expr{expression}, querySchema{{Name: "n", Usage: integer.Usage()}}, 1)
	defer owner.close()
	predicate := queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) {
		evaluations++
		if draws == 0 {
			return sqltype.Value{}, errors.New("predicate evaluated before child demand")
		}
		return sqltype.Boolean(boolean, false), nil
	})
	stage, err := openQueryNode(context.Background(), state, &filterNode{querySchema: child.Schema(), Child: child, Predicate: predicate})
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if draws != 0 || evaluations != 0 {
		t.Fatal("opening eagerly evaluated arbitrary expressions", draws, evaluations)
	}
	if _, present, err := stage.nextOccurrence(context.Background()); err != nil || present || draws != 1 || evaluations != 1 {
		t.Fatal("ordinary predicate demand changed", present, err, draws, evaluations)
	}
}

func TestQueryCTEMandatoryPhysicalFrontier(t *testing.T) {
	marked := &querySource{occurrence: 0}
	unmarked := &querySource{occurrence: 1}
	locks := &queryLockContext{leaves: []queryLockLeaf{
		{source: marked, mark: queryRowMark{Mode: lock.ForUpdate}},
		{source: unmarked},
	}}
	marked.locks, unmarked.locks = locks, locks
	inputs := &querySource{sources: []*querySource{marked, unmarked}}
	physical := &sourceNode{Occurrence: 0}
	unmarkedPhysical := &sourceNode{Occurrence: 1}
	aggregate := &aggregateNode{Child: physical}
	if len(pendingQueryMarks(aggregate, inputs, nil)) != 0 || !queryHasMandatoryPhysicalFrontier(aggregate, inputs) {
		t.Fatal("consumed physical marks mistaken for absent demand")
	}
	storedInputs := &querySource{sources: []*querySource{unmarked, marked}}
	stored := &querySource{nestedRoot: &projectNode{Child: &aggregateNode{Child: &sourceNode{Occurrence: 1}}}, nestedInputs: storedInputs}
	adapter := &querySource{input: stored}
	if !queryHasMandatoryPhysicalFrontier(&sourceNode{}, adapter) {
		t.Fatal("stored adapter lost nestedInputs occurrence mapping")
	}
	ordinary := &derivedNode{Query: &BoundQuery{Root: &projectNode{Child: aggregate}}}
	if !queryHasMandatoryPhysicalFrontier(ordinary, inputs) {
		t.Fatal("ordinary derived boundary hid mandatory input")
	}
	owners := []LogicalNode{
		&sortNode{Child: physical}, aggregate, &distinctNode{Child: physical},
		&distinctOnNode{Child: physical}, &setNode{Left: &projectNode{Child: physical}, Right: &projectNode{Child: unmarkedPhysical}},
		&limitNode{Child: physical}, &windowNode{Child: physical},
	}
	for _, owner := range owners {
		if !queryHasMandatoryPhysicalFrontier(owner, inputs) {
			t.Fatalf("consuming owner %T lost marked input", owner)
		}
	}
	if queryHasMandatoryPhysicalFrontier(&aggregateNode{Child: unmarkedPhysical}, inputs) ||
		queryHasMandatoryPhysicalFrontier(&joinNode{Left: physical, Right: &aggregateNode{Child: unmarkedPhysical}}, inputs) {
		t.Fatal("unrelated marked branch contaminated unmarked owner")
	}
	locks.identityOnly = true
	if queryHasMandatoryPhysicalFrontier(aggregate, inputs) {
		t.Fatal("identity-only manifest treated as semantic locking")
	}
	locks.identityOnly = false
	for _, hint := range []parser.CTEMaterialization{parser.CTEDefault, parser.CTEMaterialized, parser.CTENotMaterialized} {
		definition := &cteDefinition{Hint: hint, Query: ordinary.Query}
		opaque := &derivedNode{Query: ordinary.Query, CTE: definition}
		if queryHasMandatoryPhysicalFrontier(opaque, inputs) ||
			queryHasMandatoryPhysicalFrontier(&aggregateNode{Child: opaque}, inputs) {
			t.Fatal("consumer inspected opaque CTE body's physical frontier", hint)
		}
		if !queryHasMandatoryPhysicalFrontier(&joinNode{Left: opaque, Right: aggregate}, inputs) {
			t.Fatal("opaque CTE hid independently reachable physical owner", hint)
		}
		analysisOnly := &projectNode{Child: &singletonNode{}, AnalysisExpressions: []expr.Expr{subqueryExpression{cteDefinition: true, QueryPlan: &BoundQuery{Root: &projectNode{Child: aggregate}}}}}
		if queryHasMandatoryPhysicalFrontier(analysisOnly, inputs) {
			t.Fatal("unused analysis definition became executable demand")
		}
	}
}

func TestQueryCTEDeadZeroWidthOwner(t *testing.T) {
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	_, state, child := cteUnitScan(nil, querySchema{}, 2)
	state.ctes = nil
	state.source.locks = &queryLockContext{leaves: []queryLockLeaf{{mark: queryRowMark{Mode: lock.ForUpdate}}}}
	filter := &filterNode{querySchema: child.Schema(), Child: child, Predicate: literalExpression{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}
	root := &projectNode{querySchema: child.Schema(), Child: filter, Frame: 1, CTEOwner: true}
	stage, err := openQueryNode(context.Background(), state, root)
	if err != nil {
		t.Fatal(err)
	}
	owner := state.ctes
	if owner == nil || len(stage.schema) != 0 || len(owner.producers) != 0 {
		t.Fatal("zero-width gate lost owner/schema or opened producer", owner, stage.schema)
	}
	if _, present, err := stage.nextOccurrence(context.Background()); err != nil || present {
		t.Fatal("zero-width dead consumer retained occurrences", present, err)
	}
	if err := stage.Close(); err != nil || !owner.closed || len(owner.producers) != 0 {
		t.Fatal("dead consumer did not close empty owner", owner, err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal("repeated empty owner close", err)
	}
}
