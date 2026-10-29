package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func inspectionBound(t *testing.T, stat bool, e *core.Engine) functions.FTSInspection {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	name, text := "ts_debug", "Rats"
	if stat {
		name, text = "ts_stat", "SELECT NULL::tsvector"
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := node.(functions.FTSInspection)
	if !ok {
		t.Fatal(node)
	}
	return n
}
func inspectionLifecycle(t *testing.T, stat bool) {
	n := inspectionBound(t, stat, engine.New())
	want := expr.Stable
	if stat {
		want = expr.Volatile
	}
	if n.Validate() != nil || n.IntrinsicVolatility() != want || n.Properties().Volatility != want || n.PlanningFold() != expr.FoldNever || !expr.IsSetSource(n) {
		t.Fatal("inspection admission or stable ownership", n)
	}
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil || !n.SameSourceOccurrence(rebuilt) {
		t.Fatal("reconstruction", err)
	}
	if _, err := n.Render(); err != nil {
		t.Fatal(err)
	}
	if captured, ok, err := n.Capture(expr.CaptureFunction); err != nil || !ok || captured.Count != 1 {
		t.Fatal("capture", captured, err)
	}
	if child, err := n.CaptureOperand(0); err != nil || child != n.Args[0] {
		t.Fatal("child ownership", err)
	}
	bad := n
	bad.Order[0] = 1
	if bad.Validate() == nil {
		t.Fatal("invalid formal admitted")
	}
	bad = n
	bad.Count = 0
	if bad.Validate() == nil {
		t.Fatal("invalid count admitted")
	}
}
func TestFTSDebugLifecycle(t *testing.T) { inspectionLifecycle(t, false) }
func TestFTSStatLifecycle(t *testing.T) {
	inspectionLifecycle(t, true)
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	outer, description := sequencePreparedDescription(t, e, s, "SELECT * FROM ts_stat('SELECT ''x:1''::tsvector')")
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	// Direct host calls bypass Execute's root gate. Gate the genuine outer
	// statement so an idle session begins its real transaction incarnation;
	// attachment alone would merely copy the idle owner's zero TxRef.
	if err := scope.Gate(outer); err != nil {
		t.Fatal(err)
	}
	if scope.TxRef.Epoch() != e.CatalogRead().Epoch() {
		t.Fatal("root transaction was not admitted at the current catalog epoch")
	}
	n := inspectionBound(t, true, e)
	shape := n.ResultUsage().Type
	expected := &description
	scope.Expected = expected
	view := scope.View
	demand := core.ScopeSQLRoutine(scope)
	first, err := scope.WriteHost().StatTextSearch(t.Context(), "SELECT 'x:1'::tsvector", "", shape)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Expected != expected || scope.View != view || !reflect.DeepEqual(core.ScopeSQLRoutine(scope), demand) {
		t.Fatal("success retained nested scratch")
	}
	saved := append([]sqltype.Value(nil), first...)
	second, err := scope.WriteHost().StatTextSearch(t.Context(), "SELECT 'x:1'::tsvector", "", shape)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) > 0 && len(second) > 0 {
		second[0] = sqltype.Null(shape)
		if first[0] != saved[0] {
			t.Fatal("statistics invocations share mutable rows")
		}
	}
	failed, err := scope.WriteHost().StatTextSearch(t.Context(), "SELECT missing::tsvector", "", shape)
	if failed != nil || err == nil || scope.Expected != expected || scope.View != view || !reflect.DeepEqual(core.ScopeSQLRoutine(scope), demand) {
		t.Fatal("failure retained nested scratch", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	failed, err = scope.WriteHost().StatTextSearch(canceled, "SELECT 'x:1'::tsvector", "", shape)
	if failed != nil || !errors.Is(err, context.Canceled) || scope.Expected != expected || scope.View != view || !reflect.DeepEqual(core.ScopeSQLRoutine(scope), demand) {
		t.Fatal("cancellation retained nested scratch", err)
	}
}

func TestFTSDebugCaptureOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET default_text_search_config='english'")
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	n := inspectionBound(t, false, e)
	shape := n.ResultUsage().Type
	first, err := scope.WriteHost().DebugTextSearch(t.Context(), false, sqltype.Value{}, "The Rats", shape)
	if err != nil || len(first) != 3 {
		t.Fatal("first acquisition", len(first), err)
	}
	second, err := scope.WriteHost().DebugTextSearch(t.Context(), false, sqltype.Value{}, "Rats", shape)
	if err != nil || len(second) != 1 {
		t.Fatal("second acquisition", len(second), err)
	}
	saved := first[2]
	second[0] = sqltype.Null(shape)
	if first[2] != saved || first[2].IsNull() {
		t.Fatal("invocations share mutable rows")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	rows, err := scope.WriteHost().DebugTextSearch(canceled, false, sqltype.Value{}, "Rats", shape)
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("partial acquisition escaped", rows, err)
	}
	if first[2] != saved {
		t.Fatal("cancellation mutated accepted rows")
	}
}
