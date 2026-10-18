package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func regexpSetUnitTypes(t *testing.T) (CatalogRead, *sqltype.Descriptor) {
	t.Helper()
	_, view, text := regexpSetUnitEngineTypes(t)
	return view, text
}

// regexpSetUnitEngineTypes is regexpSetUnitTypes with the engine that owns the
// returned view.
func regexpSetUnitEngineTypes(t *testing.T) (*Engine, CatalogRead, *sqltype.Descriptor) {
	t.Helper()
	engine := New()
	view := engine.CatalogRead()
	text, ok := view.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing TEXT")
	}
	return engine, view, text
}
func regexpSetUnitLiteral(text *sqltype.Descriptor, s string) BoundExpr {
	return literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, s)}
}
func regexpSetUnitCall(t *testing.T, text *sqltype.Descriptor, source BoundExpr, pattern string) functions.RegexpCall {
	t.Helper()
	args := []BoundExpr{source, regexpSetUnitLiteral(text, pattern)}
	plan, err := sqltype.BindRegexpFunction("regexp_split_to_table", []sqltype.Usage{text.Usage(), text.Usage()}, text.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return functions.RegexpCall{Info: expressionInfo{Usage: text.Usage(), Pos: parser.Position{Offset: source.SourcePosition().Offset + 1}}, Plan: plan, Args: args, WrittenSlots: [7]uint8{0, 1}}
}
func regexpSetUnitRows(t *testing.T, stage *indexedStage) [][]string {
	t.Helper()
	defer stage.Close()
	var rows [][]string
	for {
		values := make([]sqltype.Value, len(stage.Schema()))
		present, err := stage.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return rows
		}
		row := make([]string, len(values))
		for i, value := range values {
			if value.IsNull() {
				row[i] = "<NULL>"
				continue
			}
			s, err := value.Text()
			if err != nil {
				t.Fatal(err)
			}
			row[i] = s
		}
		rows = append(rows, row)
	}
}
func regexpSetUnitSources(calls ...functions.RegexpCall) []BoundExpr {
	out := make([]BoundExpr, len(calls))
	for i, call := range calls {
		out[i] = functions.Regexp{RegexpCall: call}
		if call.Plan.ReturnsSet() {
			out[i] = functions.RegexpSet{RegexpCall: call}
		}
	}
	return out
}
func regexpSetUnitLayout(n *setProjectNode) *setProjectNode {
	n.baseWidth, n.markerWidth = len(n.child.Schema()), len(n.calls)
	for i := range n.calls {
		n.callSlots = append(n.callSlots, i)
		n.steps = append(n.steps, querySetStep{kind: querySetCallStep, index: i})
	}
	return n
}
func regexpSetUnitLower(engine *Engine, root LogicalNode, expressions []BoundExpr, frame uint32) (LogicalNode, []BoundExpr, error) {
	found := false
	for _, expression := range expressions {
		found = found || querySetDepth(expression) != 0
	}
	if !found {
		return root, expressions, nil
	}
	outputs := append([]BoundExpr(nil), expressions...)
	b := &expressionBinder{engine: engine, ctx: context.Background(), queryFrame: frame}
	root, err := b.lowerQuerySets(root, outputs, nil, false)
	return root, outputs, err
}
func functionSetUnitArgument(t *testing.T, calls []BoundExpr, callIndex, argumentIndex int, value BoundExpr) {
	t.Helper()
	args := append([]BoundExpr(nil), functionCallArguments(calls[callIndex])...)
	args[argumentIndex] = value
	next, err := withFunctionCallArguments(calls[callIndex], args)
	if err != nil {
		t.Fatal(err)
	}
	calls[callIndex] = next
}
func TestRegexpSetProjection(t *testing.T) {
	_, text := regexpSetUnitTypes(t)
	a := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a b"), " ")}
	b := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x"), ",")}
	n := regexpSetUnitLayout(&setProjectNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, child: &singletonNode{}, calls: []BoundExpr{a, b}, frame: 1})
	state := &queryState{source: &querySource{materialized: true}, frame: 1}
	stage, err := openSetProjectNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"a", "x"}, {"b", "<NULL>"}}) {
		t.Fatal(got)
	}
	t.Run("producer-schema-under-scalar-projection", func(t *testing.T) {
		view, text := regexpSetUnitTypes(t)
		integer, ok := view.LookupType(sqltype.Int4)
		if !ok {
			t.Fatal("missing INT4")
		}
		column := columnExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, scope: 1, frame: 1, slot: 0}
		left := &valuesNode{querySchema: querySchema{{Usage: text.Usage()}}, rows: [][]BoundExpr{{regexpSetUnitLiteral(text, "a bb")}}}
		correlated := regexpSetUnitCall(t, text, column, " ")
		function := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(correlated), frame: 1, correlated: true}
		projectSet := regexpSetUnitLayout(&setProjectNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, child: left, calls: []BoundExpr{functions.RegexpSet{RegexpCall: correlated}}, frame: 1})
		join := &joinNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, left: left, right: function, leftWidth: 1, rightWidth: 1, kind: parser.JoinInner, correlatedRight: true, frame: 1}
		standalone := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a bb"), " ")), frame: 1}
		for _, tc := range []struct {
			name string
			root LogicalNode
			slot int
		}{
			{"project-set", projectSet, 1}, {"function-from", standalone, 0}, {"dependent-join", join, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				setColumn := column
				setColumn.slot = tc.slot
				plan, err := sqltype.BindString("length", integer.Usage(), text.Usage(), []sqltype.Usage{text.Usage()}, [4]bool{true}, false)
				if err != nil {
					t.Fatal(err)
				}
				length := functions.String{Info: expressionInfo{Usage: integer.Usage()}, Plan: plan, Args: []BoundExpr{setColumn}, Textual: [4]bool{true}}
				project := &projectNode{querySchema: querySchema{{Usage: integer.Usage()}}, child: tc.root, expressions: []BoundExpr{length}, frame: 1}
				stage, err := openQueryProjectStage(context.Background(), state, project)
				if err != nil {
					t.Fatal(err)
				}
				defer stage.Close()
				values := make([]sqltype.Value, 1)
				for _, want := range []int64{1, 2} {
					present, err := stage.Next(context.Background(), values)
					if err != nil || !present {
						t.Fatal(present, err)
					}
					got, err := values[0].Integer()
					if err != nil || got != want || values[0].Type() != integer {
						t.Fatal(got, want, err)
					}
				}
				if present, err := stage.Next(context.Background(), values); err != nil || present {
					t.Fatal(present, err)
				}
			})
		}
	})
	null := literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.Null(text)}
	a = functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, null, " ")}
	n.calls = []BoundExpr{a, a}
	stage, err = openSetProjectNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := regexpSetUnitRows(t, &stage); len(got) != 0 {
		t.Fatal(got)
	}
	t.Run("rows-from-ordinality", func(t *testing.T) {
		view, _ := regexpSetUnitTypes(t)
		bigint, ok := view.LookupType(sqltype.Int8)
		if !ok {
			t.Fatal("missing INT8")
		}
		from := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: bigint.Usage()}}, calls: regexpSetUnitSources(regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a b"), " "), regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x"), ",")), ordinality: true, frame: 1}
		stage, err := openFunctionFromNode(context.Background(), state, from)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		row := make([]sqltype.Value, 3)
		for i := range 2 {
			present, err := stage.Next(context.Background(), row)
			if err != nil || !present {
				t.Fatal(present, err)
			}
			ordinal, err := row[2].Integer()
			if err != nil || ordinal != int64(i+1) {
				t.Fatal(ordinal, err)
			}
			if i == 1 && !row[1].IsNull() {
				t.Fatal("exhausted sibling not NULL")
			}
		}
		if present, err := stage.Next(context.Background(), row); err != nil || present {
			t.Fatal(present, err)
		}
	})
	t.Run("scalar-null-one-row", func(t *testing.T) {
		view, _ := regexpSetUnitTypes(t)
		for _, tc := range []struct {
			name   string
			result sqltype.ID
		}{
			{"regexp_count", sqltype.Int4}, {"regexp_instr", sqltype.Int4}, {"regexp_like", sqltype.Bool},
			{"regexp_match", 1009}, {"regexp_replace", sqltype.Text}, {"regexp_split_to_array", 1009},
		} {
			name := tc.name
			args := []BoundExpr{null, regexpSetUnitLiteral(text, "a")}
			if name == "regexp_replace" {
				args = append(args, regexpSetUnitLiteral(text, "x"))
			}
			typ, ok := view.LookupType(tc.result)
			if !ok {
				t.Fatal(tc.result)
			}
			usages := make([]sqltype.Usage, len(args))
			for i := range usages {
				usages[i] = text.Usage()
			}
			plan, err := sqltype.BindRegexpFunction(name, usages, typ.Usage(), text.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			call := functions.RegexpCall{Info: expressionInfo{Usage: typ.Usage()}, Args: args, Plan: plan, WrittenSlots: [7]uint8{0, 1, 2}}
			from := &functionFromNode{querySchema: querySchema{{Usage: typ.Usage()}}, calls: regexpSetUnitSources(call), frame: 1}
			stage, err := openFunctionFromNode(context.Background(), state, from)
			if err != nil {
				t.Fatal(err)
			}
			row := make([]sqltype.Value, 1)
			present, err := stage.Next(context.Background(), row)
			if err != nil || !present || !row[0].IsNull() || row[0].Type() != typ {
				t.Fatal(name, present, row, err)
			}
			if present, err = stage.Next(context.Background(), row); err != nil || present {
				t.Fatal(name, present, err)
			}
			stage.Close()
		}
	})
}
func TestRegexpSetNested(t *testing.T) {
	engine, _, text := regexpSetUnitEngineTypes(t)
	inner := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a b,c d"), ",")}
	outer := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, inner, " ")}
	root, outputs, err := regexpSetUnitLower(engine, &singletonNode{}, []BoundExpr{outer}, 1)
	if err != nil {
		t.Fatal(err)
	}
	top, ok := root.(*setProjectNode)
	if !ok {
		t.Fatalf("not project set: %T", root)
	}
	if _, ok := top.child.(*setProjectNode); !ok {
		t.Fatalf("not layered: %T", top.child)
	}
	state := &queryState{source: &querySource{materialized: true}, frame: 1}
	stage, err := openQueryNode(context.Background(), state, root)
	if err != nil {
		t.Fatal(err)
	}
	stage.schema = querySchema{{Usage: text.Usage()}}
	stage.expressions = outputs
	stage.evaluation = state.evaluation(context.Background())
	if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"a"}, {"b"}, {"c"}, {"d"}}) {
		t.Fatal(got)
	}
	t.Run("scalar-wrapper", func(t *testing.T) {
		plan, err := sqltype.BindString("upper", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage()}, [4]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		wrapper := functions.String{Info: expressionInfo{Usage: text.Usage()}, Plan: plan, Args: []BoundExpr{outer}, Textual: [4]bool{true}}
		root, outputs, err := regexpSetUnitLower(engine, &singletonNode{}, []BoundExpr{wrapper}, 1)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := openQueryNode(context.Background(), state, root)
		if err != nil {
			t.Fatal(err)
		}
		stage.schema = querySchema{{Usage: text.Usage()}}
		stage.expressions = outputs
		stage.evaluation = state.evaluation(context.Background())
		if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"A"}, {"B"}, {"C"}, {"D"}}) {
			t.Fatal(got)
		}
	})
	// Scalar siblings are demanded for every expanded occurrence, not once per
	// input or once when a vector is drained.
	calls := 0
	n := regexpSetUnitLayout(&setProjectNode{querySchema: querySchema{{Usage: text.Usage()}}, child: &singletonNode{}, calls: []BoundExpr{inner}, frame: 1})
	stage, err = openSetProjectNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	stage.expressions = []BoundExpr{queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.String(text, "draw"), nil })}
	stage.evaluation = state.evaluation(context.Background())
	regexpSetUnitRows(t, &stage)
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestRegexpSetCorrelation(t *testing.T) {
	_, text := regexpSetUnitTypes(t)
	column := columnExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, scope: 1, frame: 1, slot: 0}
	call := regexpSetUnitCall(t, text, column, " ")
	right := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(call), frame: 1, correlated: true}
	left := &valuesNode{querySchema: querySchema{{Usage: text.Usage()}}, rows: [][]BoundExpr{{regexpSetUnitLiteral(text, "a b")}, {literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.Null(text)}}}}
	n := &joinNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, left: left, right: right, leftWidth: 1, rightWidth: 1, kind: parser.JoinLeft, frame: 1, correlatedRight: true}
	state := &queryState{source: &querySource{materialized: true}, frame: 1}
	stage, err := openJoinNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	got := regexpSetUnitRows(t, &stage)
	if !reflect.DeepEqual(got, [][]string{{"a b", "a"}, {"a b", "b"}, {"<NULL>", "<NULL>"}}) {
		t.Fatal(got)
	}
	t.Run("nested-prior-left-prefix", func(t *testing.T) {
		local := column
		local.slot = 1
		functions := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, calls: regexpSetUnitSources(regexpSetUnitCall(t, text, column, " "), regexpSetUnitCall(t, text, local, ",")), frame: 1, correlated: true}
		localLeft := &valuesNode{querySchema: querySchema{{Usage: text.Usage()}}, rows: [][]BoundExpr{{regexpSetUnitLiteral(text, "x,y")}}}
		nested := &joinNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, left: localLeft, right: functions, leftWidth: 1, rightWidth: 2, kind: parser.JoinInner, frame: 1, correlatedRight: true, correlationPrefix: 1}
		parentLeft := &valuesNode{querySchema: querySchema{{Usage: text.Usage()}}, rows: [][]BoundExpr{{regexpSetUnitLiteral(text, "a b")}}}
		parent := &joinNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, left: parentLeft, right: nested, leftWidth: 1, rightWidth: 3, kind: parser.JoinInner, frame: 1, correlatedRight: true}
		stage, err := openJoinNode(context.Background(), state, parent)
		if err != nil {
			t.Fatal(err)
		}
		if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"a b", "x,y", "a", "x"}, {"a b", "x,y", "b", "y"}}) {
			t.Fatal(got)
		}
	})
}
func TestRegexpSetOwnership(t *testing.T) {
	_, text := regexpSetUnitTypes(t)
	call := regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a b"), " ")
	n := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(call), frame: 1}
	state := &queryState{source: &querySource{materialized: true}, frame: 1}
	stage, err := openFunctionFromNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]sqltype.Value, 1)
	present, err := stage.Next(context.Background(), values)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	held := values[0]
	if changed, err := stage.rewind(context.Background()); err != nil || !changed && stage.position != 0 {
		t.Fatal("rewind lost cached prefix")
	}
	if present, err := stage.Next(context.Background(), values); err != nil || !present {
		t.Fatal(present, err)
	}
	stage.Close()
	if got, err := held.Text(); err != nil || got != "a" {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stage, err = openFunctionFromNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Next(ctx, values); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if stage.source != nil {
		t.Fatal("failed stage retained source")
	}
	// An argument error remains the primary failure during stage cleanup.
	failure := errors.New("selected invocation failed")
	functionSetUnitArgument(t, n.calls, 0, 0, queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, failure }))
	stage, err = openFunctionFromNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stage.Next(context.Background(), values); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if stage.source != nil {
		t.Fatal("error retained producer")
	}
}
func TestRegexpSetWalks(t *testing.T) {
	engine, view, text := regexpSetUnitEngineTypes(t)
	column := columnExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, scope: 1, frame: 1, slot: 0}
	set := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, column, " ")}
	input := &valuesNode{querySchema: querySchema{{Usage: text.Usage()}}, rows: [][]BoundExpr{{regexpSetUnitLiteral(text, "a b")}}}
	root, _, err := regexpSetUnitLower(engine, input, []BoundExpr{set}, 1)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := rewriteQueryExpressions(root, func(expr BoundExpr) (BoundExpr, bool, error) {
		return mapBoundExpression(view, expr, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil, func(node BoundExpr) (BoundExpr, bool, error) {
			literal, ok := node.(literalExpression)
			if !ok || literal.value.IsNull() {
				return node, false, nil
			}
			value, err := literal.value.Text()
			if err != nil || value != " " {
				return node, false, nil
			}
			literal.value = sqltype.String(text, ",")
			return literal, true, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := openQueryNode(context.Background(), &queryState{source: &querySource{materialized: true}, frame: 1}, rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"a b", "a b"}}) {
		t.Fatal(got)
	}
}

// Binding can remove an unused inherited prefix without changing a stored
// caller's owned argument tree, a different lexical frame, or a private virtual
// owner recipe. Exercise the actual invocation owners with divergent rows.
func TestRegexpCorrelationFrameRebase(t *testing.T) {
	view, text := regexpSetUnitTypes(t)
	column := func(frame uint32, slot int) columnExpression {
		return columnExpression{expressionInfo: expressionInfo{Usage: text.Usage(), Property: ExprProperties{RowDependent: true}}, scope: 1, frame: frame, slot: slot}
	}
	capture := func(frame uint32, slot int) []functionCorrelation {
		return []functionCorrelation{{frame: frame, slot: slot, name: "captured"}}
	}
	row := func(values ...string) []sqltype.Value {
		out := make([]sqltype.Value, len(values))
		for i, value := range values {
			out[i] = sqltype.String(text, value)
		}
		return out
	}
	run := func(t *testing.T, node LogicalNode, frame uint32, values []sqltype.Value, outer *EvalContext) [][]string {
		t.Helper()
		evaluation := &EvalContext{Context: context.Background(), Scope: 1, Frame: frame, Row: values, Outer: outer}
		state := &queryState{source: &querySource{materialized: true}, frame: frame, invocation: evaluation, outer: outer}
		stage, err := openQueryNode(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		return regexpSetUnitRows(t, &stage)
	}
	t.Run("owned-arguments-and-distinct-outer-frame", func(t *testing.T) {
		n := &functionFromNode{
			querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}},
			calls:       regexpSetUnitSources(regexpSetUnitCall(t, text, column(7, 2), " "), regexpSetUnitCall(t, text, column(9, 1), " ")),
			frame:       7, correlated: true, captures: capture(7, 2),
		}
		rebased, err := rebaseFunctionCorrelation(view, n, 7, 2)
		if err != nil {
			t.Fatal(err)
		}
		outer := &EvalContext{Context: context.Background(), Scope: 1, Frame: 9, Row: row("outer-unused", "x y")}
		want := [][]string{{"a", "x"}, {"b", "y"}}
		if got := run(t, rebased, 7, row("a b"), outer); !reflect.DeepEqual(got, want) {
			t.Fatal("rebased lexical invocation", got)
		}
		if got := run(t, n, 7, row("prefix-one", "prefix-two", "a b"), outer); !reflect.DeepEqual(got, want) {
			t.Fatal("original owned arguments changed", got)
		}
	})
	t.Run("already-local-right-and-free-left", func(t *testing.T) {
		left := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(regexpSetUnitCall(t, text, column(7, 2), " ")), frame: 7, correlated: true, captures: capture(7, 2)}
		right := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(regexpSetUnitCall(t, text, column(7, 0), "")), frame: 7, correlated: true, captures: capture(7, 0)}
		n := &joinNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, left: left, right: right, leftWidth: 1, rightWidth: 1, kind: parser.JoinInner, frame: 7, correlatedRight: true, captures: capture(7, 2)}
		rebased, err := rebaseFunctionCorrelation(view, n, 7, 2)
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"aa", "a"}, {"aa", "a"}, {"bb", "b"}, {"bb", "b"}}
		if got := run(t, rebased, 7, row("aa bb"), nil); !reflect.DeepEqual(got, want) {
			t.Fatal("already local invocation was rebased twice", got)
		}
		if got := run(t, n, 7, row("prefix-one", "prefix-two", "aa bb"), nil); !reflect.DeepEqual(got, want) {
			t.Fatal("original captured-left invocation changed", got)
		}
	})
	t.Run("private-virtual-owner-window", func(t *testing.T) {
		virtual := column(1, 3)
		virtual.virtual, virtual.rowStart, virtual.rowWidth = column(1, 0), 2, 2
		n := &functionFromNode{querySchema: querySchema{{Usage: text.Usage()}}, calls: regexpSetUnitSources(regexpSetUnitCall(t, text, virtual, " ")), frame: 1, correlated: true, captures: capture(1, 3)}
		rebased, err := rebaseFunctionCorrelation(view, n, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"alpha"}, {"beta"}}
		if got := run(t, rebased, 1, row("alpha beta", "physical-placeholder"), nil); !reflect.DeepEqual(got, want) {
			t.Fatal("private recipe or owner row window changed", got)
		}
		if got := run(t, n, 1, row("prefix-one", "prefix-two", "alpha beta", "physical-placeholder"), nil); !reflect.DeepEqual(got, want) {
			t.Fatal("original virtual owner window changed", got)
		}
	})
}

func TestFunctionSourceMixedRows(t *testing.T) {
	engine, view, text := regexpSetUnitEngineTypes(t)
	b := &expressionBinder{engine: engine, ctx: context.Background(), queryFrame: 1}
	integer, _ := view.LookupType(sqltype.Int4)
	bigint, _ := view.LookupType(sqltype.Int8)
	for _, tc := range []struct {
		name, input string
		want        [][]string
	}{
		{"unequal", "a,b", [][]string{{"a", "x"}, {"b", "y"}, {"<NULL>", "z"}}},
		{"empty-peer", "", [][]string{{"<NULL>", "x"}, {"<NULL>", "y"}, {"<NULL>", "z"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stringCall := stringSplitMarker(t, b, 40, regexpSetUnitLiteral(text, tc.input), regexpSetUnitLiteral(text, ","))
			regexpCall := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x_y_z"), "_")}
			null := literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.Null(text)}
			plan, err := sqltype.BindRegexpFunction("regexp_count", []sqltype.Usage{text.Usage(), text.Usage()}, integer.Usage(), text.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			scalar := functions.Regexp{RegexpCall: regexpCallForNull(plan, integer, null, regexpSetUnitLiteral(text, "["))}
			node := &functionFromNode{calls: []BoundExpr{stringCall, regexpCall, scalar}, frame: 1, ordinality: true,
				querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: integer.Usage()}, {Usage: bigint.Usage()}}}
			stage, err := openFunctionFromNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			values := make([]sqltype.Value, 4)
			for i, want := range tc.want {
				present, err := stage.Next(context.Background(), values)
				if err != nil || !present {
					t.Fatal(present, err)
				}
				for j := range 2 {
					got := "<NULL>"
					if !values[j].IsNull() {
						got, err = values[j].Text()
						if err != nil {
							t.Fatal(err)
						}
					}
					if got != want[j] || values[j].Type() != text {
						t.Fatal(i, j, got, want[j], values[j].Type())
					}
				}
				ordinal, err := values[3].Integer()
				if err != nil || ordinal != int64(i+1) || !values[2].IsNull() || values[2].Type() != integer {
					t.Fatal(values, err)
				}
			}
			if present, err := stage.Next(context.Background(), values); err != nil || present {
				t.Fatal(present, err)
			}
		})
	}
}
func regexpCallForNull(plan sqltype.RegexpPlan, result *sqltype.Descriptor, source, pattern BoundExpr) functions.RegexpCall {
	return functions.RegexpCall{Info: expressionInfo{Usage: result.Usage()}, Plan: plan, Args: []BoundExpr{source, pattern}, WrittenSlots: [7]uint8{0, 1}}
}

func TestQuerySetMixedAttemptOwnership(t *testing.T) {
	engine, _, text := regexpSetUnitEngineTypes(t)
	for _, upper := range []bool{false, true} {
		t.Run(map[bool]string{false: "bare-terminal-attempt", true: "upper-wrapper-emitted-only"}[upper], func(t *testing.T) {
			b := &expressionBinder{engine: engine, ctx: context.Background(), queryFrame: 1}
			stringCall := stringSplitMarker(t, b, 40, regexpSetUnitLiteral(text, "a,b"), regexpSetUnitLiteral(text, ","))
			regexpCall := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x_y_z"), "_")}
			draws := 0
			scalar := regexpSetUnitLiteral(text, "draw")
			var selected BoundExpr = regexpCall
			if upper {
				plan, err := sqltype.BindString("upper", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage()}, [4]bool{true}, false)
				if err != nil {
					t.Fatal(err)
				}
				selected = functions.String{Info: expressionInfo{Usage: text.Usage()}, Plan: plan, Args: []BoundExpr{selected}, Textual: [4]bool{true}}
			}
			outputs := []BoundExpr{scalar, stringCall, selected}
			root, err := b.lowerQuerySets(&singletonNode{}, outputs, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			probe := queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.String(text, "draw"), nil })
			if upper {
				outputs[0] = probe
			} else {
				root.(*setProjectNode).scalars[0] = probe
			}
			project := &projectNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, child: root, expressions: outputs, frame: 1}
			stage, err := openQueryProjectStage(context.Background(), &queryState{source: &querySource{}, frame: 1}, project)
			if err != nil {
				t.Fatal(err)
			}
			want := [][]string{{"draw", "a", "x"}, {"draw", "b", "y"}, {"draw", "<NULL>", "z"}}
			if upper {
				for i := range want {
					want[i][2] = []string{"X", "Y", "Z"}[i]
				}
			}
			if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
			expectedDraws := 4
			if upper {
				expectedDraws = 3
			}
			if draws != expectedDraws {
				t.Fatal(draws, expectedDraws)
			}
		})
	}
}

func TestFunctionRecheckOwnedRegexpCursor(t *testing.T) {
	_, text := regexpSetUnitTypes(t)
	draws := 0
	source := queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.String(text, "a b"), nil })
	constant := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, source, " ")}
	column := columnExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, scope: 1, frame: 1, slot: 0}
	dependent := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, column, " ")}
	node := &functionFromNode{calls: []BoundExpr{constant, dependent}, frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}}
	var cache functionRecheckCache
	defer cache.close()
	evaluation := &EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
	var held sqltype.Value
	for i, input := range []string{"x y", "m n"} {
		evaluation.Row = []sqltype.Value{sqltype.String(text, input)}
		invocation, err := cache.open(evaluation, node)
		if err != nil {
			t.Fatal(err)
		}
		values := make([]sqltype.Value, 2)
		for j := range 2 {
			present, err := invocation.next(context.Background(), node.calls, values, nil)
			if err != nil || !present {
				invocation.close()
				t.Fatal(present, err)
			}
			left, err := values[0].Text()
			if err != nil || left != []string{"a", "b"}[j] {
				invocation.close()
				t.Fatal(left, err)
			}
			right, err := values[1].Text()
			want := [][]string{{"x", "y"}, {"m", "n"}}[i][j]
			if err != nil || right != want {
				invocation.close()
				t.Fatal(right, want, err)
			}
			if i == 0 && j == 0 {
				held = values[0]
			}
		}
		if present, err := invocation.next(context.Background(), node.calls, values, nil); err != nil || present {
			invocation.close()
			t.Fatal(present, err)
		}
		invocation.close()
	}
	cache.close()
	if value, err := held.Text(); err != nil || value != "a" || draws != 1 {
		t.Fatal(value, draws, err)
	}
}
