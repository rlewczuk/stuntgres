package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func regexpSetUnitTypes(t *testing.T) (catalog.Read, *sqltype.Descriptor) {
	t.Helper()
	_, view, text := regexpSetUnitEngineTypes(t)
	return view, text
}

// regexpSetUnitEngineTypes is regexpSetUnitTypes with the engine that owns the
// returned view.
func regexpSetUnitEngineTypes(t *testing.T) (*Engine, catalog.Read, *sqltype.Descriptor) {
	t.Helper()
	engine := New()
	view := engine.CatalogRead()
	text, ok := view.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing TEXT")
	}
	return engine, view, text
}
func regexpSetUnitLiteral(text *sqltype.Descriptor, s string) expr.Expr {
	return plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, s)}
}
func regexpSetUnitCall(t *testing.T, text *sqltype.Descriptor, source expr.Expr, pattern string) functions.RegexpCall {
	t.Helper()
	args := []expr.Expr{source, regexpSetUnitLiteral(text, pattern)}
	plan, err := sqltype.BindRegexpFunction("regexp_split_to_table", []sqltype.Usage{text.Usage(), text.Usage()}, text.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return functions.RegexpCall{Info: expr.Info{Usage: text.Usage(), Pos: parser.Position{Offset: source.SourcePosition().Offset + 1}}, Plan: plan, Args: args, WrittenSlots: [7]uint8{0, 1}}
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
func regexpSetUnitSources(calls ...functions.RegexpCall) []expr.Expr {
	out := make([]expr.Expr, len(calls))
	for i, call := range calls {
		out[i] = functions.Regexp{RegexpCall: call}
		if call.Plan.ReturnsSet() {
			out[i] = functions.RegexpSet{RegexpCall: call}
		}
	}
	return out
}
func regexpSetUnitLayout(n *plan.SetProjectNode) *plan.SetProjectNode {
	n.BaseWidth, n.MarkerWidth = len(n.Child.Schema()), len(n.Calls)
	for i := range n.Calls {
		n.CallSlots = append(n.CallSlots, i)
		n.Steps = append(n.Steps, plan.SetStep{Kind: querySetCallStep, Index: i})
	}
	return n
}
func regexpSetUnitLower(engine *Engine, root plan.Node, expressions []expr.Expr, frame uint32) (plan.Node, []expr.Expr, error) {
	found := false
	for _, expression := range expressions {
		found = found || querySetDepth(expression) != 0
	}
	if !found {
		return root, expressions, nil
	}
	outputs := append([]expr.Expr(nil), expressions...)
	b := &expressionBinder{host: engine.analysisHost(nil), ctx: context.Background(), query: queryScopeState{frame: frame}}
	root, err := b.lowerQuerySets(root, outputs, nil, false)
	return root, outputs, err
}
func functionSetUnitArgument(t *testing.T, calls []expr.Expr, callIndex, argumentIndex int, value expr.Expr) {
	t.Helper()
	args := append([]expr.Expr(nil), expr.Expr(calls[callIndex]).(expr.Composite).Children()...)
	args[argumentIndex] = value
	next, err := expr.Substitute(expr.Expr(calls[callIndex]).(expr.Composite), expr.InfoOf(calls[callIndex]), args)
	if err != nil {
		t.Fatal(err)
	}
	calls[callIndex] = next
}
func TestRegexpSetProjection(t *testing.T) {
	_, text := regexpSetUnitTypes(t)
	a := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a b"), " ")}
	b := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x"), ",")}
	n := regexpSetUnitLayout(&plan.SetProjectNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, Child: &plan.SingletonNode{}, Calls: []expr.Expr{a, b}, Frame: 1})
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
		column := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Scope: 1, Frame: 1, Slot: 0}
		left := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Rows: [][]expr.Expr{{regexpSetUnitLiteral(text, "a bb")}}}
		correlated := regexpSetUnitCall(t, text, column, " ")
		function := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(correlated), Frame: 1, Correlated: true}
		projectSet := regexpSetUnitLayout(&plan.SetProjectNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, Child: left, Calls: []expr.Expr{functions.RegexpSet{RegexpCall: correlated}}, Frame: 1})
		join := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, Left: left, Right: function, LeftWidth: 1, RightWidth: 1, Kind: parser.JoinInner, CorrelatedRight: true, Frame: 1}
		standalone := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a bb"), " ")), Frame: 1}
		for _, tc := range []struct {
			name string
			root plan.Node
			slot int
		}{
			{"project-set", projectSet, 1}, {"function-from", standalone, 0}, {"dependent-join", join, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				setColumn := column
				setColumn.Slot = tc.slot
				stringPlan, err := sqltype.BindString("length", integer.Usage(), text.Usage(), []sqltype.Usage{text.Usage()}, [4]bool{true}, false)
				if err != nil {
					t.Fatal(err)
				}
				length := functions.String{Info: expr.Info{Usage: integer.Usage()}, Plan: stringPlan, Args: []expr.Expr{setColumn}, Textual: [4]bool{true}}
				project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}}, Child: tc.root, Expressions: []expr.Expr{length}, Frame: 1}
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
	null := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
	a = functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, null, " ")}
	n.Calls = []expr.Expr{a, a}
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
		from := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: bigint.Usage()}}, Calls: regexpSetUnitSources(regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "a b"), " "), regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x"), ",")), Ordinality: true, Frame: 1}
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
			args := []expr.Expr{null, regexpSetUnitLiteral(text, "a")}
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
			regexpPlan, err := sqltype.BindRegexpFunction(name, usages, typ.Usage(), text.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			call := functions.RegexpCall{Info: expr.Info{Usage: typ.Usage()}, Args: args, Plan: regexpPlan, WrittenSlots: [7]uint8{0, 1, 2}}
			from := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: typ.Usage()}}, Calls: regexpSetUnitSources(call), Frame: 1}
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
	root, outputs, err := regexpSetUnitLower(engine, &plan.SingletonNode{}, []expr.Expr{outer}, 1)
	if err != nil {
		t.Fatal(err)
	}
	top, ok := root.(*plan.SetProjectNode)
	if !ok {
		t.Fatalf("not project set: %T", root)
	}
	if _, ok := top.Child.(*plan.SetProjectNode); !ok {
		t.Fatalf("not layered: %T", top.Child)
	}
	state := &queryState{source: &querySource{materialized: true}, frame: 1}
	stage, err := openQueryNode(context.Background(), state, root)
	if err != nil {
		t.Fatal(err)
	}
	stage.schema = plan.QuerySchema{{Usage: text.Usage()}}
	stage.expressions = outputs
	stage.evaluation = state.evaluation(context.Background())
	if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"a"}, {"b"}, {"c"}, {"d"}}) {
		t.Fatal(got)
	}
	t.Run("scalar-wrapper", func(t *testing.T) {
		stringPlan, err := sqltype.BindString("upper", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage()}, [4]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		wrapper := functions.String{Info: expr.Info{Usage: text.Usage()}, Plan: stringPlan, Args: []expr.Expr{outer}, Textual: [4]bool{true}}
		root, outputs, err := regexpSetUnitLower(engine, &plan.SingletonNode{}, []expr.Expr{wrapper}, 1)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := openQueryNode(context.Background(), state, root)
		if err != nil {
			t.Fatal(err)
		}
		stage.schema = plan.QuerySchema{{Usage: text.Usage()}}
		stage.expressions = outputs
		stage.evaluation = state.evaluation(context.Background())
		if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"A"}, {"B"}, {"C"}, {"D"}}) {
			t.Fatal(got)
		}
	})
	// Scalar siblings are demanded for every expanded occurrence, not once per
	// input or once when a vector is drained.
	calls := 0
	n := regexpSetUnitLayout(&plan.SetProjectNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Child: &plan.SingletonNode{}, Calls: []expr.Expr{inner}, Frame: 1})
	stage, err = openSetProjectNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	stage.expressions = []expr.Expr{queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.String(text, "draw"), nil })}
	stage.evaluation = state.evaluation(context.Background())
	regexpSetUnitRows(t, &stage)
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestRegexpSetCorrelation(t *testing.T) {
	_, text := regexpSetUnitTypes(t)
	column := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	call := regexpSetUnitCall(t, text, column, " ")
	right := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(call), Frame: 1, Correlated: true}
	left := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Rows: [][]expr.Expr{{regexpSetUnitLiteral(text, "a b")}, {plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}}}}
	n := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, Left: left, Right: right, LeftWidth: 1, RightWidth: 1, Kind: parser.JoinLeft, Frame: 1, CorrelatedRight: true}
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
		local.Slot = 1
		functions := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, Calls: regexpSetUnitSources(regexpSetUnitCall(t, text, column, " "), regexpSetUnitCall(t, text, local, ",")), Frame: 1, Correlated: true}
		localLeft := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Rows: [][]expr.Expr{{regexpSetUnitLiteral(text, "x,y")}}}
		nested := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, Left: localLeft, Right: functions, LeftWidth: 1, RightWidth: 2, Kind: parser.JoinInner, Frame: 1, CorrelatedRight: true, CorrelationPrefix: 1}
		parentLeft := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Rows: [][]expr.Expr{{regexpSetUnitLiteral(text, "a b")}}}
		parent := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, Left: parentLeft, Right: nested, LeftWidth: 1, RightWidth: 3, Kind: parser.JoinInner, Frame: 1, CorrelatedRight: true}
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
	n := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(call), Frame: 1}
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
	functionSetUnitArgument(t, n.Calls, 0, 0, queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, failure }))
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
	column := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	set := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, column, " ")}
	input := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Rows: [][]expr.Expr{{regexpSetUnitLiteral(text, "a b")}}}
	root, _, err := regexpSetUnitLower(engine, input, []expr.Expr{set}, 1)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := rewriteQueryExpressions(root, func(bound expr.Expr) (expr.Expr, bool, error) {
		return mapBoundExpression(view, bound, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil, func(node expr.Expr) (expr.Expr, bool, error) {
			literal, ok := node.(plan.LiteralExpr)
			if !ok || literal.Value.IsNull() {
				return node, false, nil
			}
			value, err := literal.Value.Text()
			if err != nil || value != " " {
				return node, false, nil
			}
			literal.Value = sqltype.String(text, ",")
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
	column := func(frame uint32, slot int) plan.ColumnExpr {
		return plan.ColumnExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: frame, Slot: slot}
	}
	capture := func(frame uint32, slot int) []plan.FunctionCorrelation {
		return []plan.FunctionCorrelation{{Frame: frame, Slot: slot, Name: "captured"}}
	}
	row := func(values ...string) []sqltype.Value {
		out := make([]sqltype.Value, len(values))
		for i, value := range values {
			out[i] = sqltype.String(text, value)
		}
		return out
	}
	run := func(t *testing.T, node plan.Node, frame uint32, values []sqltype.Value, outer *EvalContext) [][]string {
		t.Helper()
		evaluation := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: frame, Row: values, Outer: outer}}
		state := &queryState{source: &querySource{materialized: true}, frame: frame, invocation: evaluation, outer: outer}
		stage, err := openQueryNode(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		return regexpSetUnitRows(t, &stage)
	}
	t.Run("owned-arguments-and-distinct-outer-frame", func(t *testing.T) {
		n := &plan.FunctionFromNode{
			QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}},
			Calls:       regexpSetUnitSources(regexpSetUnitCall(t, text, column(7, 2), " "), regexpSetUnitCall(t, text, column(9, 1), " ")),
			Frame:       7, Correlated: true, Captures: capture(7, 2),
		}
		rebased, err := rebaseFunctionCorrelation(view, n, 7, 2)
		if err != nil {
			t.Fatal(err)
		}
		outer := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 9, Row: row("outer-unused", "x y")}}
		want := [][]string{{"a", "x"}, {"b", "y"}}
		if got := run(t, rebased, 7, row("a b"), outer); !reflect.DeepEqual(got, want) {
			t.Fatal("rebased lexical invocation", got)
		}
		if got := run(t, n, 7, row("prefix-one", "prefix-two", "a b"), outer); !reflect.DeepEqual(got, want) {
			t.Fatal("original owned arguments changed", got)
		}
	})
	t.Run("already-local-right-and-free-left", func(t *testing.T) {
		left := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(regexpSetUnitCall(t, text, column(7, 2), " ")), Frame: 7, Correlated: true, Captures: capture(7, 2)}
		right := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(regexpSetUnitCall(t, text, column(7, 0), "")), Frame: 7, Correlated: true, Captures: capture(7, 0)}
		n := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, Left: left, Right: right, LeftWidth: 1, RightWidth: 1, Kind: parser.JoinInner, Frame: 7, CorrelatedRight: true, Captures: capture(7, 2)}
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
		virtual.Virtual, virtual.RowStart, virtual.RowWidth = column(1, 0), 2, 2
		n := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}}, Calls: regexpSetUnitSources(regexpSetUnitCall(t, text, virtual, " ")), Frame: 1, Correlated: true, Captures: capture(1, 3)}
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
	b := &expressionBinder{host: engine.analysisHost(nil), ctx: context.Background(), query: queryScopeState{frame: 1}}
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
			null := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
			regexpPlan, err := sqltype.BindRegexpFunction("regexp_count", []sqltype.Usage{text.Usage(), text.Usage()}, integer.Usage(), text.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			scalar := functions.Regexp{RegexpCall: regexpCallForNull(regexpPlan, integer, null, regexpSetUnitLiteral(text, "["))}
			node := &plan.FunctionFromNode{Calls: []expr.Expr{stringCall, regexpCall, scalar}, Frame: 1, Ordinality: true,
				QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: integer.Usage()}, {Usage: bigint.Usage()}}}
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
func regexpCallForNull(plan sqltype.RegexpPlan, result *sqltype.Descriptor, source, pattern expr.Expr) functions.RegexpCall {
	return functions.RegexpCall{Info: expr.Info{Usage: result.Usage()}, Plan: plan, Args: []expr.Expr{source, pattern}, WrittenSlots: [7]uint8{0, 1}}
}

func TestQuerySetMixedAttemptOwnership(t *testing.T) {
	engine, _, text := regexpSetUnitEngineTypes(t)
	for _, upper := range []bool{false, true} {
		t.Run(map[bool]string{false: "bare-terminal-attempt", true: "upper-wrapper-emitted-only"}[upper], func(t *testing.T) {
			b := &expressionBinder{host: engine.analysisHost(nil), ctx: context.Background(), query: queryScopeState{frame: 1}}
			stringCall := stringSplitMarker(t, b, 40, regexpSetUnitLiteral(text, "a,b"), regexpSetUnitLiteral(text, ","))
			regexpCall := functions.RegexpSet{RegexpCall: regexpSetUnitCall(t, text, regexpSetUnitLiteral(text, "x_y_z"), "_")}
			draws := 0
			scalar := regexpSetUnitLiteral(text, "draw")
			var selected expr.Expr = regexpCall
			if upper {
				plan, err := sqltype.BindString("upper", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage()}, [4]bool{true}, false)
				if err != nil {
					t.Fatal(err)
				}
				selected = functions.String{Info: expr.Info{Usage: text.Usage()}, Plan: plan, Args: []expr.Expr{selected}, Textual: [4]bool{true}}
			}
			outputs := []expr.Expr{scalar, stringCall, selected}
			root, err := b.lowerQuerySets(&plan.SingletonNode{}, outputs, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			probe := queryProbe(text.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return sqltype.String(text, "draw"), nil })
			if upper {
				outputs[0] = probe
			} else {
				root.(*plan.SetProjectNode).Scalars[0] = probe
			}
			project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, Child: root, Expressions: outputs, Frame: 1}
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
