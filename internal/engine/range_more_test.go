package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func moreUnitExpression(t *testing.T, view CatalogRead) unnestExpression {
	t.Helper()
	m, _ := view.LookupType(sqltype.Int4Multirange)
	r, _ := view.LookupType(sqltype.Int4Range)
	v, err := m.Input(context.Background(), "{[8,9),[1,3)}", -1)
	if err != nil {
		t.Fatal(err)
	}
	return unnestExpression{
		Info: expressionInfo{Usage: r.Usage(), Pos: parser.Position{Offset: 7, Character: 8}, Property: ExprProperties{Volatility: Immutable, StageDependent: true}},
		args: []BoundExpr{literalExpression{Info: expressionInfo{Usage: m.Usage()}, value: v}},
	}
}

func TestRangeMoreExpressionCursor(t *testing.T) {
	view := New().CatalogRead()
	e := moreUnitExpression(t, view)
	if !isQuerySet(e) || !validFunctionSourceCall(e) || len(functionCallArguments(e)) != 1 {
		t.Fatal("missing SRF classification")
	}
	c := &EvalContext{Context: context.Background()}
	if _, err := e.Eval(c); err == nil {
		t.Fatal("unlifted scalar execution")
	}
	count := 0
	value := e.args[0].(literalExpression).value
	e.args[0] = queryProbe(e.args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { count++; return value, nil })
	first, err := openFunctionCall(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openFunctionCall(c, e)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	if count != 2 {
		t.Fatal("argument not once per invocation", count)
	}
	for _, tc := range []struct {
		cursor *functionSetCursor
		want   string
	}{{&first, "[1,3)"}, {&first, "[8,9)"}, {&second, "[1,3)"}} {
		v, present, err := tc.cursor.next(c.Context, e.ResultUsage().Type)
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
	if v, present, err := first.next(c.Context, e.ResultUsage().Type); err != nil || present || !v.IsNull() {
		t.Fatal(v, present, err)
	}
	ctx, cancel := context.WithCancel(c.Context)
	cancel()
	if _, _, err := second.next(ctx, e.ResultUsage().Type); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	second.close()
	second.close()
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
	n := next.(unnestExpression)
	if n.ResultUsage().Type == e.ResultUsage().Type || n.args[0].ResultUsage().Type == e.args[0].ResultUsage().Type || n.Properties() != e.Properties() || n.SourcePosition() != e.SourcePosition() {
		t.Fatal("retag lost authority or properties")
	}
	if sameQuerySetOccurrence(e, n) || !sameQuerySetOccurrence(e, e) {
		t.Fatal("occurrence identity")
	}
	mapped, changed, err := e.mapChildren(func(BoundExpr) (BoundExpr, bool, error) { return n.args[0], true, nil })
	if err != nil || !changed || mapped.args[0].ResultUsage() != n.args[0].ResultUsage() || e.args[0].ResultUsage() == n.args[0].ResultUsage() {
		t.Fatal("child map mutated source", err)
	}
	if _, err := withFunctionCallArguments(e, nil); err == nil {
		t.Fatal("invalid replacement arity")
	}
}

func TestRangeMoreBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
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
		arg := literalExpression{Info: expressionInfo{Usage: d.Usage()}, value: value}
		raw := nullCountRaw("multirange")
		node, err := functions.FinishRangeInspect(b, raw, true, []functions.Argument{{Expression: arg, Usage: d.Usage()}})
		if err != nil || node.ResultUsage().Type != b.typ(tc.output) {
			t.Fatal(node, err)
		}
		got, err := node.Eval(&EvalContext{Context: context.Background()})
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
