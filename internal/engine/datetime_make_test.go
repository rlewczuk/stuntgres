package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func datetimeMakeRaw(name string, values ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: values, Pos: parser.Position{Character: 8}}
}
func datetimeMakeInteger(n int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
}
func datetimeMakeString(s string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
}

func TestDatetimeMakeBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		name   string
		args   []parser.Expr
		result sqltype.ID
		count  int
	}{
		{"make_date", []parser.Expr{datetimeMakeInteger(2013), datetimeMakeInteger(7), datetimeMakeInteger(15)}, sqltype.Date, 3},
		{"make_time", []parser.Expr{datetimeMakeInteger(8), datetimeMakeInteger(15), datetimeMakeString("23.5")}, sqltype.Time, 3},
		{"make_timestamp", []parser.Expr{datetimeMakeInteger(2013), datetimeMakeInteger(7), datetimeMakeInteger(15), datetimeMakeInteger(8), datetimeMakeInteger(15), datetimeMakeString("23.5")}, sqltype.Timestamp, 6},
		{"make_timestamptz", []parser.Expr{datetimeMakeInteger(2013), datetimeMakeInteger(7), datetimeMakeInteger(15), datetimeMakeInteger(8), datetimeMakeInteger(15), datetimeMakeString("23.5")}, sqltype.Timestamptz, 6},
		{"make_interval", nil, sqltype.Interval, 7},
	} {
		node, err := b.bindDatetimeMake(datetimeMakeRaw(tc.name, tc.args...), true)
		if err != nil {
			t.Fatal(tc, err)
		}
		e := node.(datetimeMakeExpression)
		if e.count != tc.count || e.ResultUsage().Type.ID() != tc.result || e.ResultUsage().Typmod != -1 {
			t.Fatal(tc, e)
		}
	}
	raw := datetimeMakeRaw("make_interval", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2})
	raw.ArgNames = []parser.Identifier{{Text: "secs"}, {Text: "days"}}
	node, err := b.bindDatetimeMake(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	e := node.(datetimeMakeExpression)
	if e.writtenSlots[0] != 6 || e.writtenSlots[1] != 3 || b.parameters[1].Type.ID() != sqltype.Float8 || b.parameters[2].Type.ID() != sqltype.Int4 {
		t.Fatal(e, b.parameters)
	}
	for _, tc := range []struct {
		raw  parser.FunctionExpr
		code string
	}{
		{datetimeMakeRaw("make_date", datetimeMakeInteger(2000)), "42883"},
		{datetimeMakeRaw("make_date", parser.CastExpr{Child: datetimeMakeInteger(2000), TypeName: parser.TypeName{Name: "int8"}}, datetimeMakeInteger(1), datetimeMakeInteger(1)), "42883"},
		{datetimeMakeRaw("make_interval", parser.CastExpr{Child: datetimeMakeString("bad"), TypeName: parser.TypeName{Name: "text"}}), "42883"},
	} {
		_, err := b.bindDatetimeMake(tc.raw, true)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
}

func TestDatetimeMakeProperties(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval"} {
		_, _, count, _ := datetimeMakeSignature(name)
		if name == "make_timestamptz" {
			count = 6
		}
		var args []parser.Expr
		for i := range count {
			n := int64(0)
			if i < 3 && name != "make_time" && name != "make_interval" {
				n = 1
			}
			args = append(args, datetimeMakeInteger(n))
		}
		node, err := b.bindDatetimeMake(datetimeMakeRaw(name, args...), true)
		if err != nil {
			t.Fatal(name, err)
		}
		e := node.(datetimeMakeExpression)
		stable := name == "make_timestamptz"
		want := Immutable
		if stable {
			want = Stable
		}
		if e.Properties().Volatility != want || plannedVolatility(e) != want || generationImmutable(e) == stable {
			t.Fatal(name, e.Properties(), plannedVolatility(e))
		}
		if !b.sameGroupExpression(e, e) {
			t.Fatal("lost structural identity", name)
		}
		replacement := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacement[d.ID()] = d
		}
		next, changed, err := retagBoundTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
		if err != nil || !changed {
			t.Fatal(name, next, changed, err)
		}
		rebound := next.(datetimeMakeExpression)
		if rebound.ResultUsage().Type != replacement[e.usage.Type.ID()] || e.ResultUsage().Type == rebound.ResultUsage().Type {
			t.Fatal("descriptor authority", name)
		}
		for i := range rebound.children() {
			if rebound.args[i].ResultUsage().Type == e.args[i].ResultUsage().Type {
				t.Fatal("retained old child descriptor", name, i)
			}
		}
	}
	raw := datetimeMakeRaw("make_timestamptz", datetimeMakeInteger(2000), datetimeMakeInteger(1), datetimeMakeInteger(1), datetimeMakeInteger(0), datetimeMakeInteger(0), datetimeMakeInteger(0), datetimeMakeString("UTC"))
	node, err := b.bindDatetimeMake(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if plannedVolatility(node) != Stable || generationImmutable(node) {
		t.Fatal("explicit UTC incorrectly immutable")
	}
	// Runtime NULL must not suppress a demanded sibling, and source display order
	// never controls the declared-slot execution order.
	interval, err := b.bindDatetimeMake(datetimeMakeRaw("make_interval"), true)
	if err != nil {
		t.Fatal(err)
	}
	e := interval.(datetimeMakeExpression)
	var order []int
	e.args[3] = mathObservedArgument{expressionInfo: expressionInfo{usage: b.typ(sqltype.Int4).Usage()}, value: sqltype.Null(b.typ(sqltype.Int4)), order: &order, index: 3}
	failure := errors.New("demanded seconds")
	e.args[6] = mathObservedArgument{expressionInfo: expressionInfo{usage: b.typ(sqltype.Float8).Usage()}, order: &order, index: 6, err: failure}
	if _, err := e.Eval(&EvalContext{Context: ctx}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{3, 6}) {
		t.Fatal(order, err)
	}
}

func TestDatetimeMakeArgumentAssociation(t *testing.T) {
	ctx := context.Background()
	engine := New()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	raw := datetimeMakeRaw("make_interval", datetimeMakeString("1.5"), datetimeMakeInteger(2))
	raw.ArgNames = []parser.Identifier{{Text: "secs"}, {Text: "days"}}
	node, err := b.bindDatetimeMake(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID(), presentation: viewPresentation{deparse: true, pretty: true}}
	got, err := renderer.expressionSQL(ctx, engine.CatalogRead(), raw, viewLiteralPath{clause: viewTargetLiteral})
	if err != nil || got != "make_interval(secs => '1.5'::double precision, days => 2)" {
		t.Fatal("written literal association/default omission", got, err)
	}
	e := node.(datetimeMakeExpression)
	other := e
	other.writtenSlots[0], other.writtenSlots[1] = other.writtenSlots[1], other.writtenSlots[0]
	other.labels[0], other.labels[1] = other.labels[1], other.labels[0]
	if !b.sameGroupExpression(e, other) {
		t.Fatal("display provenance changed semantic identity")
	}
}
