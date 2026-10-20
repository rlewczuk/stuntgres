package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func moreUnitExpression(t *testing.T, view catalog.Read) plan.UnnestExpr {
	t.Helper()
	m, _ := view.LookupType(sqltype.Int4Multirange)
	r, _ := view.LookupType(sqltype.Int4Range)
	v, err := m.Input(context.Background(), "{[8,9),[1,3)}", -1)
	if err != nil {
		t.Fatal(err)
	}
	return plan.UnnestExpr{
		Info: expr.Info{Usage: r.Usage(), Pos: parser.Position{Offset: 7, Character: 8}, Property: expr.Properties{Volatility: expr.Immutable, StageDependent: true}},
		Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: m.Usage()}, Value: v}},
	}
}

func TestRangeMoreExpressionCursor(t *testing.T) {
	view := New().CatalogRead()
	e := moreUnitExpression(t, view)
	if !SourceReturnsSet(e) || !SourceAdmitted(e) || len(expr.Expr(e).(expr.Composite).Children()) != 1 {
		t.Fatal("missing SRF classification")
	}
	c := &EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	if _, err := e.Eval(c); err == nil {
		t.Fatal("unlifted scalar execution")
	}
	count := 0
	value := e.Args[0].(plan.LiteralExpr).Value
	e.Args[0] = queryProbe(e.Args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { count++; return value, nil })
	first, err := OpenFunctionSource(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenFunctionSource(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if count != 2 {
		t.Fatal("argument not once per invocation", count)
	}
	for _, tc := range []struct {
		cursor *expr.SourceCursor
		want   string
	}{{&first, "[1,3)"}, {&first, "[8,9)"}, {&second, "[1,3)"}} {
		v, present, err := tc.cursor.Next(c.Context)
		if err != nil || !present {
			t.Fatal(v, present, err)
		}
		text, err := v.Type().Output(c.Context, v)
		if err != nil || text != tc.want {
			t.Fatal(text, tc.want, err)
		}
	}
	if count != 2 {
		t.Fatal("argument re-evaluated per member", count)
	}
	if v, present, err := first.Next(c.Context); err != nil || present || !v.IsNull() {
		t.Fatal(v, present, err)
	}
	ctx, cancel := context.WithCancel(c.Context)
	cancel()
	if _, _, err := second.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	second.Close()
	second.Close()
}

func TestRangeMoreExpressionHooks(t *testing.T) {
	view, fresh := New().CatalogRead(), New().CatalogRead()
	e := moreUnitExpression(t, view)
	next, changed, err := retagBoundTypes(view, e, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		n, ok := fresh.LookupType(d.ID())
		if ok {
			return n
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	n := next.(plan.UnnestExpr)
	if n.ResultUsage().Type == e.ResultUsage().Type || n.Args[0].ResultUsage().Type == e.Args[0].ResultUsage().Type || n.Properties() != e.Properties() || n.SourcePosition() != e.SourcePosition() {
		t.Fatal("retag lost authority or properties")
	}
	if sameQuerySetOccurrence(e, n) || !sameQuerySetOccurrence(e, e) {
		t.Fatal("occurrence identity")
	}
	rewritten, changed, err := expr.MapSubstituted(e, func(expr.Expr) (expr.Expr, bool, error) { return n.Args[0], true, nil })
	mapped := rewritten.(plan.UnnestExpr)
	if err != nil || !changed || mapped.Args[0].ResultUsage() != n.Args[0].ResultUsage() || e.Args[0].ResultUsage() == n.Args[0].ResultUsage() {
		t.Fatal("child map mutated source", err)
	}
	if _, err := expr.Substitute(expr.Expr(e).(expr.Composite), expr.InfoOf(e), nil); err == nil {
		t.Fatal("invalid replacement arity")
	}
}

func TestRangeMoreBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.clause.aggregateClause = "SELECT"
	for _, tc := range []struct {
		input, output sqltype.ID
		text, want    string
	}{
		{sqltype.Int4Range, sqltype.Int4Multirange, "[1,4]", "{[1,5)}"},
		{sqltype.Int8Range, sqltype.Int8Multirange, "[1,4]", "{[1,5)}"},
		{sqltype.NumRange, sqltype.NumMultirange, "(1.00,2.000]", "{(1.00,2.000]}"},
	} {
		d := b.typ(tc.input)
		value, err := d.Input(context.Background(), tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		arg := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
		raw := nullCountRaw("multirange")
		node, err := functions.FinishRangeInspect(b, raw, true, []functions.Argument{{Expression: arg, Usage: d.Usage()}})
		if err != nil || node.ResultUsage().Type != b.typ(tc.output) {
			t.Fatal(node, err)
		}
		got, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := got.Type().Output(context.Background(), got)
		if err != nil || text != tc.want {
			t.Fatal(text, err)
		}
	}
	raw := nullCountRaw("unnest", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
	raw.Pos = parser.Position{Character: 8}
	_, err := b.bindUnnest(raw)
	inspectBindingDiagnostic(t, err, "42725", "function unnest(unknown) is not unique", "8")
	for _, id := range []sqltype.ID{sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange} {
		raw := nullCountRaw("multirange")
		raw.Pos = parser.Position{Character: 8}
		d := b.typ(id)
		_, err := functions.FinishRangeInspect(b, raw, true, []functions.Argument{{Usage: d.Usage()}})
		inspectBindingDiagnostic(t, err, "0A000", "temporal multirange construction is unsupported", "8")
	}
}
