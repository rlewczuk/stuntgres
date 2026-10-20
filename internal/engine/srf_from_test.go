package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func srfFromUnitNode(t *testing.T, calls ...expr.Expr) *plan.FunctionFromNode {
	t.Helper()
	n := &plan.FunctionFromNode{Calls: calls, Frame: 1, Ordinality: true}
	for _, call := range calls {
		n.QuerySchema = append(n.QuerySchema, catalog.Column{Usage: call.ResultUsage()})
	}
	n.QuerySchema = append(n.QuerySchema, catalog.Column{Name: "ordinality", Usage: seriesUnitType(t, sqltype.Int8).Usage()})
	return n
}
func srfFromUnitState() *queryState {
	return &queryState{Source: &querySource{Materialized: true}, Frame: 1}
}
func srfFromUnitRows(t *testing.T, n plan.Node) [][]any {
	t.Helper()
	stage, err := exec.OpenNode(context.Background(), srfFromUnitState(), n)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	var rows [][]any
	values := make([]sqltype.Value, len(stage.Schema()))
	for {
		present, err := stage.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return rows
		}
		row := make([]any, len(values))
		for i, v := range values {
			if v.Type() != stage.Schema()[i].Usage.Type {
				t.Fatal("lost member type", i, v)
			}
			if !v.IsNull() {
				row[i], err = v.Integer()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		rows = append(rows, row)
	}
}

func TestSRFFromBinding(t *testing.T) {
	lit := func(n int64) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
	}
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.query.auth = &auth
	b.query.frame = 1
	raw := nullCountRaw("generate_series", lit(2), lit(4))
	node, ns, err := b.bindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{raw}, Ordinality: true})
	if err != nil {
		t.Fatal(err)
	}
	source, ok := node.(*plan.FunctionFromNode)
	if !ok || !source.RegexpNamespace || b.clause.queryHasTargetSet || len(ns.columns) != 2 || ns.columns[0].descriptor.Name != "generate_series" || ns.columns[1].descriptor.Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(node, ns)
	}
	if got := srfFromUnitRows(t, source); !reflect.DeepEqual(got, [][]any{{int64(2), int64(1)}, {int64(3), int64(2)}, {int64(4), int64(3)}}) {
		t.Fatal(got)
	}
	// Binding a scalar wrapper is diagnostic reachability, not source eligibility.
	nested := raw
	nested.Pos = parser.Position{Character: 19, Offset: 18}
	_, _, err = b.bindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{nullCountRaw("abs", nested)}})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "set-returning functions must appear at top level of FROM" || diagnostic.Position != "19" {
		t.Fatal(err)
	}
	_, _, err = b.bindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{nullCountRaw("abs", lit(1))}})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "function table source is unsupported" {
		t.Fatal("admitted scalar source", err)
	}
	// Real successful completion order selects the last sibling, while an
	// earlier nested failure stops analysis before a later sibling.
	at := func(position int, name string, args ...parser.Expr) parser.FunctionExpr {
		call := nullCountRaw(name, args...)
		call.Pos = parser.Position{Character: position, Offset: position - 1}
		return call
	}
	first := at(31, "generate_series", lit(1), lit(2))
	last := at(61, "generate_series", lit(3), lit(4))
	text := func(value string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: value}}
	}
	regexpFirst := at(41, "regexp_split_to_table", text("a,b"), text(","))
	regexpLast := at(81, "regexp_split_to_table", text("c,d"), text(","))
	for _, tc := range []struct {
		call     parser.FunctionExpr
		position string
	}{
		{at(15, "generate_series", first, last), "61"},
		{at(15, "generate_series", at(25, "abs", first), at(55, "abs", last)), "61"},
		{at(15, "generate_series", at(25, "generate_series", lit(1), first), last), "31"},
		{at(15, "regexp_split_to_table", regexpFirst, regexpLast), "81"},
		{at(15, "generate_series", first, parser.SubqueryExpr{Kind: parser.SubqueryScalar, Query: &parser.Select{Targets: []parser.Expr{last}}}), "31"},
	} {
		_, _, err := b.bindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{tc.call}})
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Position != tc.position || diagnostic.Message != "set-returning functions must appear at top level of FROM" {
			t.Fatal("completion-order placement", tc.position, err)
		}
	}
	for _, tc := range []struct {
		calls                         []parser.FunctionExpr
		ordinality                    bool
		code, message, hint, position string
	}{
		{[]parser.FunctionExpr{raw}, false, "42601", `a column definition list is only allowed for functions returning "record"`, "", "101"},
		{[]parser.FunctionExpr{raw}, true, "42601", "WITH ORDINALITY cannot be used with a column definition list", "Put the column definition list inside ROWS FROM().", "101"},
		{[]parser.FunctionExpr{raw, raw}, true, "42601", "ROWS FROM() with multiple functions cannot have a column definition list", "Put a separate column definition list for each function inside ROWS FROM().", "101"},
		{[]parser.FunctionExpr{at(15, "generate_series", first, last)}, true, "0A000", "set-returning functions must appear at top level of FROM", "", "61"},
		{[]parser.FunctionExpr{raw, at(15, "generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: false}})}, true, "42883", "function generate_series(boolean, boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "15"},
	} {
		alias := &parser.AliasReference{Input: &parser.FunctionTable{Calls: tc.calls, RowsFrom: len(tc.calls) > 1, Ordinality: tc.ordinality}, Name: parser.Identifier{Text: "g"}, ColumnDefinitions: []parser.FunctionColumnDefinition{{Name: parser.Identifier{Text: "v", Pos: parser.Position{Character: 101, Offset: 100}}, Type: parser.TypeName{Name: "integer"}}}}
		_, _, err := b.bindFrom(alias)
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Hint != tc.hint || diagnostic.Position != tc.position {
			t.Fatal("deferred column definitions", tc, err)
		}
	}
}

func TestSRFFromInvocation(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int4)
	big := seriesUnitType(t, sqltype.Int8)
	view := New().CatalogRead()
	subs := subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: -2, Length: 2}}, 1)
	n := srfFromUnitNode(t, seriesUnit(t, d, 1, 3), seriesUnit(t, big, 10, 10), subs)
	want := [][]any{{int64(1), int64(10), int64(-2), int64(1)}, {int64(2), nil, int64(-1), int64(2)}, {int64(3), nil, nil, int64(3)}}
	for range 2 {
		if got := srfFromUnitRows(t, n); !reflect.DeepEqual(got, want) {
			t.Fatal("padding/reset", got)
		}
	}
	empty := srfFromUnitNode(t, seriesUnit(t, d, 3, 1), subscriptsUnit(t, view, nil, 1))
	if got := srfFromUnitRows(t, empty); len(got) != 0 {
		t.Fatal("synthetic all-empty row", got)
	}
	// Once per invocation, in written order, never once per emitted row.
	call := seriesUnit(t, d, 1, 3, 1)
	var order []int
	for i, arg := range call.Args {
		index, value := i, arg.(plan.LiteralExpr).Value
		call.Args[i] = queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) { order = append(order, index); return value, nil })
	}
	probed := srfFromUnitNode(t, call)
	for range 2 {
		if got := srfFromUnitRows(t, probed); len(got) != 3 {
			t.Fatal(got)
		}
	}
	if !reflect.DeepEqual(order, []int{0, 1, 2, 0, 1, 2}) {
		t.Fatal("argument evaluation", order)
	}
	order = nil
	zero := seriesUnit(t, big, 0, 0).Args[0]
	limit := &plan.LimitNode{QuerySchema: probed.QuerySchema, Child: probed, Count: zero}
	if got := srfFromUnitRows(t, limit); len(got) != 0 || len(order) != 0 {
		t.Fatal("LIMIT0 opened provider", got, order)
	}
}

func TestSRFFromOwnership(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int4)
	n := srfFromUnitNode(t, seriesUnit(t, d, 1, 3))
	stage, err := exec.OpenFunctionFromNode(context.Background(), srfFromUnitState(), n)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	row := make([]sqltype.Value, 2)
	if present, err := stage.Next(context.Background(), row); err != nil || !present {
		t.Fatal(present, err)
	}
	retained := row[0]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stage.Next(ctx, row); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if stage.Source != nil {
		t.Fatal("cancel retained producer")
	}
	if value, err := retained.Integer(); err != nil || value != 1 {
		t.Fatal("retained value changed", value, err)
	}
	failure := errors.New("second source argument failed")
	bad := seriesUnit(t, d, 1, 2)
	bad.Args[0] = queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, failure })
	// First member opens successfully; failed partial open must close the group.
	invocation, err := exec.OpenFunctionInvocation(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, &plan.FunctionFromNode{Calls: []expr.Expr{n.Calls[0], bad}})
	if !errors.Is(err, failure) || len(invocation.Cursors) != 0 {
		t.Fatal(invocation, err)
	}
	failed := srfFromUnitNode(t, n.Calls[0], bad)
	stage, err = exec.OpenFunctionFromNode(context.Background(), srfFromUnitState(), failed)
	if err != nil {
		t.Fatal(err)
	}
	row = make([]sqltype.Value, 3)
	if present, err := stage.Next(context.Background(), row); !errors.Is(err, failure) || present || stage.Source != nil {
		t.Fatal("partial publication/retention", present, err)
	}
	stage.Close()
	if got := srfFromUnitRows(t, n); !reflect.DeepEqual(got, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}) {
		t.Fatal("failed execution mutated plan", got)
	}
	// Query-local observation is restored on both successful and failed child
	// analysis, including child FROM calls that complete before failure.
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.query.auth = &auth
	b.clause.aggregateClause = "FROM"
	parentCall := nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}})
	parentCall.Pos = parser.Position{Character: 19, Offset: 18}
	if _, err := b.bindFunction(parentCall); err != nil {
		t.Fatal(err)
	}
	parent := b.clause.fromSetObservation
	childCall := parentCall
	childCall.Pos = parser.Position{Character: 71, Offset: 70}
	for _, fails := range []bool{false, true} {
		target := parser.Expr(parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}})
		if fails {
			target = nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: false}})
		}
		_, err := b.bindSelect(&parser.Select{From: &parser.FunctionTable{Calls: []parser.FunctionExpr{childCall}}, Targets: []parser.Expr{target}}, false)
		if (err != nil) != fails || b.clause.fromSetObservation != parent || b.clause.aggregateClause != "FROM" {
			t.Fatal("child query observation restoration", fails, b.clause.fromSetObservation, err)
		}
	}
}

func TestSRFFromStoredEligibility(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int4)
	view := New().CatalogRead()
	calls := []expr.Expr{seriesUnit(t, d, 1, 2), subscriptsUnit(t, view, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, 1)}
	for _, original := range calls {
		if !exec.SourceAdmitted(original) {
			t.Fatal("stored call rejected", original)
		}
		args := append([]expr.Expr(nil), expr.Expr(original).(expr.Composite).Children()...)
		first := args[0]
		args[0] = queryProbe(first.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { return first.Eval(c) })
		mapped, err := expr.Substitute(expr.Expr(original).(expr.Composite), expr.InfoOf(original), args)
		if err != nil || !exec.SourceAdmitted(mapped) {
			t.Fatal(mapped, err)
		}
		if _, ok := expr.Expr(original).(expr.Composite).Children()[0].(plan.LiteralExpr); !ok {
			t.Fatal("immutable original overwritten")
		}
		if got := srfFromUnitRows(t, srfFromUnitNode(t, mapped)); !reflect.DeepEqual(got, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}) {
			t.Fatal(got)
		}
		if _, err := expr.Substitute(expr.Expr(original).(expr.Composite), expr.InfoOf(original), args[:1]); err == nil {
			t.Fatal("malformed remap admitted")
		}
	}
	original := srfFromUnitNode(t, calls[0])
	rewritten, err := plan.RewriteQueryExpressions(original, func(expr expr.Expr) (expr.Expr, bool, error) {
		literal, ok := expr.(plan.LiteralExpr)
		if !ok {
			return expr, false, nil
		}
		value, err := literal.Value.Integer()
		if err != nil || value != 2 {
			return expr, false, err
		}
		literal.Value, err = sqltype.Integer(d, 3)
		return literal, true, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := srfFromUnitRows(t, rewritten); len(got) != 3 {
		t.Fatal("source argument traversal", got)
	}
	if got := srfFromUnitRows(t, original); len(got) != 2 {
		t.Fatal("rewrite mutated stored source", got)
	}
}
