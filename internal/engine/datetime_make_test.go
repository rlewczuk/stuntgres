package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
		node, err := b.BindFunctionProvider(datetimeMakeRaw(tc.name, tc.args...))
		if err != nil {
			t.Fatal(tc, err)
		}
		e := node.(functions.DatetimeMake)
		if e.Count != tc.count || e.ResultUsage().Type.ID() != tc.result || e.ResultUsage().Typmod != -1 {
			t.Fatal(tc, e)
		}
	}
	raw := datetimeMakeRaw("make_interval", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2})
	raw.ArgNames = []parser.Identifier{{Text: "secs"}, {Text: "days"}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	e := node.(functions.DatetimeMake)
	if e.WrittenSlots[0] != 6 || e.WrittenSlots[1] != 3 || b.Params.Parameters[1].Type.ID() != sqltype.Float8 || b.Params.Parameters[2].Type.ID() != sqltype.Int4 {
		t.Fatal(e, b.Params.Parameters)
	}
	for _, tc := range []struct {
		raw  parser.FunctionExpr
		code string
	}{
		{datetimeMakeRaw("make_date", datetimeMakeInteger(2000)), "42883"},
		{datetimeMakeRaw("make_date", parser.CastExpr{Child: datetimeMakeInteger(2000), TypeName: parser.TypeName{Name: "int8"}}, datetimeMakeInteger(1), datetimeMakeInteger(1)), "42883"},
		{datetimeMakeRaw("make_interval", parser.CastExpr{Child: datetimeMakeString("bad"), TypeName: parser.TypeName{Name: "text"}}), "42883"},
	} {
		_, err := b.BindFunctionProvider(tc.raw)
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
		_, _, count, _ := functions.DatetimeMakeSignature(name)
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
		node, err := b.BindFunctionProvider(datetimeMakeRaw(name, args...))
		if err != nil {
			t.Fatal(name, err)
		}
		e := node.(functions.DatetimeMake)
		stable := name == "make_timestamptz"
		want := expr.Immutable
		if stable {
			want = expr.Stable
		}
		if e.Properties().Volatility != want || exec.PlannedVolatility(e) != want || plan.GenerationImmutable(e) == stable {
			t.Fatal(name, e.Properties(), exec.PlannedVolatility(e))
		}
		if !b.SameGroupExpression(e, e) {
			t.Fatal("lost structural identity", name)
		}
		replacement := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacement[d.ID()] = d
		}
		next, changed, err := stored.RetagTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
		if err != nil || !changed {
			t.Fatal(name, next, changed, err)
		}
		rebound := next.(functions.DatetimeMake)
		if rebound.ResultUsage().Type != replacement[e.Usage.Type.ID()] || e.ResultUsage().Type == rebound.ResultUsage().Type {
			t.Fatal("descriptor authority", name)
		}
		for i := range rebound.Children() {
			if rebound.Args[i].ResultUsage().Type == e.Args[i].ResultUsage().Type {
				t.Fatal("retained old child descriptor", name, i)
			}
		}
	}
	raw := datetimeMakeRaw("make_timestamptz", datetimeMakeInteger(2000), datetimeMakeInteger(1), datetimeMakeInteger(1), datetimeMakeInteger(0), datetimeMakeInteger(0), datetimeMakeInteger(0), datetimeMakeString("UTC"))
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	if exec.PlannedVolatility(node) != expr.Stable || plan.GenerationImmutable(node) {
		t.Fatal("explicit UTC incorrectly immutable")
	}

}

func TestDatetimeMakeArgumentAssociation(t *testing.T) {
	ctx := context.Background()
	engine := New()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	raw := datetimeMakeRaw("make_interval", datetimeMakeString("1.5"), datetimeMakeInteger(2))
	raw.ArgNames = []parser.Identifier{{Text: "secs"}, {Text: "days"}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := viewRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID(), Presentation: viewPresentation{Deparse: true, Pretty: true}}
	got, err := renderer.ExpressionSQL(ctx, engine.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
	if err != nil || got != "make_interval(secs => '1.5'::double precision, days => 2)" {
		t.Fatal("written literal association/default omission", got, err)
	}
	e := node.(functions.DatetimeMake)
	other := e
	other.WrittenSlots[0], other.WrittenSlots[1] = other.WrittenSlots[1], other.WrittenSlots[0]
	other.Labels[0], other.Labels[1] = other.Labels[1], other.Labels[0]
	if !b.SameGroupExpression(e, other) {
		t.Fatal("display provenance changed semantic identity")
	}
}
