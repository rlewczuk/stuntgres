package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIsnValidationExpression(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil); err != nil { t.Fatal(err) }
	for _, tc := range []struct{ role, input string }{
		{"ean13", "2222222222222!"}, {"isbn", "123456789X!"}, {"isbn13", "9781234567897!"},
		{"ismn", "M123456785!"}, {"ismn13", "9790123456785!"}, {"issn", "12345679!"}, {"issn13", "9771234567003!"}, {"upc", "123456789012!"},
	} {
		for _, name := range []string{"is_valid", "make_valid"} {
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: tc.input}}, TypeName: parser.TypeName{Name: tc.role}, Syntax: parser.PostfixCast}}}
			bound, err := b.BindFunctionProvider(raw)
			if err != nil { t.Fatal(tc.role, name, err) }
			n, ok := bound.(functions.IsnValidation)
			if !ok || n.Count != 1 || n.PlanningFold() != expr.FoldConstant || n.IntrinsicVolatility() != expr.Immutable || n.StrictNull() != expr.StrictAfterBare { t.Fatal("selected immutable node", tc.role, name) }
			if n.RoutineReference == nil || n.RoutineRef.Address.ObjectID == 0 { t.Fatal("missing real selected identity") }
			value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil { t.Fatal(err) }
			if name == "is_valid" {
				truth, err := value.Boolean()
				if err != nil || truth || value.Type().ID() != sqltype.Bool { t.Fatal("bit predicate", err) }
			} else if value.Type() != n.Args[0].ResultUsage().Type || reflect.ValueOf(value).FieldByName("integer").Int()&1 != 0 { t.Fatal("repair type/flag") }
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Check(n, 1) != nil { t.Fatal("ordinary capture", err) }
			rebuilt, err := n.Rebuild(n.Info, n.Children())
			if err != nil || !reflect.DeepEqual(rebuilt, n) { t.Fatal("immutable rebuild", err) }
			if _, err := n.Render(); err != nil { t.Fatal(err) }
			nullChild := plan.LiteralExpr{Info: expr.Info{Usage: n.Args[0].ResultUsage()}, Value: sqltype.Null(n.Args[0].ResultUsage().Type)}
			nullNode, err := n.Rebuild(n.Info, []expr.Expr{nullChild})
			if err != nil { t.Fatal(err) }
			value, err = nullNode.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("strict result framing", err) }
			text, _ := e.CatalogRead().LookupType(sqltype.Text)
			bad := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "bad")}
			if _, err := n.Rebuild(n.Info, []expr.Expr{bad}); err == nil { t.Fatal("foreign formal rebuilt") }
		}
	}
	for _, args := range [][]parser.Expr{nil, {parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		bound, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "isn_weak"}}}, Args: args})
		if err != nil { t.Fatal(err) }
		n := bound.(functions.IsnValidation)
		want := expr.Stable
		if len(args) != 0 { want = expr.Volatile }
		if n.IntrinsicVolatility() != want || n.PlanningFold() != expr.FoldNever || n.Count != len(args) { t.Fatal("weak property contract") }
		if _, err := n.Rebuild(n.Info, n.Children()); err != nil { t.Fatal(err) }
		if _, err := n.Render(); err != nil { t.Fatal(err) }
	}
}

func TestIsnValidationBinding(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil); err != nil { t.Fatal(err) }
	call := func(name string, args ...parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	}
	null := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: parser.TypeName{Name: "ean13"}, Syntax: parser.PostfixCast}
	array := parser.ArrayExpr{Elements: []parser.Expr{null}}
	bad := null
	bad.Child = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "bad"}}
	missing := call("make_valid", array)
	missing.Name.Parts = append([]parser.Identifier{{Text: "iv_absent"}}, missing.Name.Parts...)
	for _, tc := range []struct{ raw parser.FunctionExpr; code string }{
		{call("make_valid", array), "42883"},
		{call("is_valid", array), "42883"},
		{call("make_valid", parser.ArrayExpr{Elements: []parser.Expr{array}}), "42883"},
		{call("make_valid", parser.ArrayExpr{Elements: []parser.Expr{bad}}), "22P02"},
		{call("make_valid", parser.ArrayExpr{Elements: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "bad"}}, null}}), "22P02"},
		{call("pg_typeof", array), "0A000"},
		{missing, "3F000"},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		node, err := b.BindFunctionProvider(tc.raw)
		var diagnostic *sqlstate.Error
		if node != nil || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code { t.Fatal("lookup-only array resolution", tc.raw, node, err) }
	}
	// Retire the real header and all unrelated casts, retaining only the two
	// genuine primitive-signature module routines. No name-index override or
	// unrelated cast is allowed to be their catalog-search prerequisite.
	for _, setter := range []bool{false, true} {
		routine := parser.GrantRoutineTarget{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "isn_weak"}}}, SignatureSpecified: true}
		if setter { routine.Arguments = []parser.GrantRoutineArgument{{Type: parser.TypeName{Name: "boolean"}}} }
		stmt := &parser.AlterExtension{Name: parser.Identifier{Text: "isn"}, Action: "drop", Member: parser.ExtensionMemberTarget{Kind: "function", Routine: routine}}
		if _, err := e.Execute(op, stmt, 0, nil); err != nil { t.Fatal(err) }
	}
	if _, err := e.Execute(op, &parser.DropExtension{Names: []parser.Identifier{{Text: "isn"}}}, 0, nil); err != nil { t.Fatal(err) }
	if e.CatalogRead().HasCasts() { t.Fatal("retirement left unrelated cast prerequisite") }
	for _, args := range [][]parser.Expr{nil, {parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		node, err := b.BindFunctionProvider(call("isn_weak", args...))
		if err != nil { t.Fatal("retired primitive-signature member", err) }
		selected, ok := node.(functions.IsnValidation)
		if !ok || selected.Count != len(args) || selected.RoutineRef.Address.ObjectID == 0 { t.Fatal("lost selected module identity") }
		view := e.CatalogRead()
		if err := extension.StockIsn.ValidateProcedure(view, view.MustProcedure(selected.RoutineRef.Address)); err != nil { t.Fatal("selected retired declaration authority", err) }
	}
	empty := engine.New()
	for _, name := range []string{"is_valid", "make_valid", "isn_weak"} {
		b := enginetest.ExpressionBinder(t, context.Background(), empty, sqltype.InputEnvironment{}, nil)
		_, err := b.BindFunctionProvider(call(name))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" { t.Fatal("name hint granted callable authority", name, err) }
	}
}
