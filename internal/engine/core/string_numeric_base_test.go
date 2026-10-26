package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStringNumericBaseBindingAndRebinding(t *testing.T) {
	e := engine.New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	call := func(name string, arg parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{arg}, Pos: parser.Position{Character: 8, Offset: 8}}
	}
	parameter := parser.ParameterExpr{Number: 1}
	for _, name := range []string{"to_bin", "to_oct", "to_hex"} {
		_, err := b.Bind(call(name, parameter), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42725" || diagnostic.Position != "8" || diagnostic.Detail != "" || diagnostic.Hint != "Could not choose a best candidate function. You might need to add explicit type casts." {
			t.Fatal(name, err)
		}
		if b.Params.Parameters[1].Type != nil {
			t.Fatal("ambiguous call fixed parameter")
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Int8} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Params.Parameters[1] = b.Type(id).Usage()
		node, err := b.Bind(call("to_hex", parameter), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		selected, ok := node.(functions.String)
		if !ok || selected.Plan.ArgumentUsage(0).Type.ID() != id || selected.ResultUsage().Type.ID() != sqltype.Text || selected.Properties().Volatility != expr.Immutable {
			t.Fatal(node)
		}
		v, err := sqltype.Integer(b.Type(id), -1)
		if err != nil {
			t.Fatal(err)
		}
		selected.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: b.Type(id).Usage()}, Value: v}
		replacements := make(map[sqltype.ID]*sqltype.Descriptor)
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		rebound, changed, err := stored.RetagTypes(engine.New().CatalogRead(), selected, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		next := rebound.(functions.String)
		if next.Plan.ArgumentUsage(0).Type != replacements[id] || next.ResultUsage().Type != replacements[sqltype.Text] {
			t.Fatal(next)
		}
		got, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(err)
		}
		actual, failure := got.Text()
		want := "ffffffff"
		if id == sqltype.Int8 {
			want = "ffffffffffffffff"
		}
		if failure != nil || actual != want {
			t.Fatal(id, actual, failure)
		}
		if got.Type() != replacements[sqltype.Text] {
			t.Fatal("retained result generation", got)
		}
		if selected.Plan.ArgumentUsage(0).Type != b.Type(id) {
			t.Fatal("source provider mutated")
		}
	}
}

func TestStringNumericBaseMD5Rebinding(t *testing.T) {
	e := engine.New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	u := b.Type(sqltype.Text).Usage()
	value := sqltype.String(u.Type, "abc")
	arg := plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: value}
	node, err := b.StringNode("md5", u, parser.Position{}, []expr.Expr{arg}, [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	replacements := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := stored.RetagTypes(engine.New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := rebound.(functions.String)
	got, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || got.Type() != replacements[sqltype.Text] {
		t.Fatal(got, err)
	}
	actual, failure := got.Text()
	if failure != nil || actual != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatal(actual, failure)
	}
}

func TestStringNumericBaseStoredWidthReplay(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, tc := range []struct {
		name string
		int4 string
		int8 string
	}{
		{"to_bin", "11111111111111111111111111111111", "1111111111111111111111111111111111111111111111111111111111111111"},
		{"to_oct", "37777777777", "1777777777777777777777"},
		{"to_hex", "ffffffff", "ffffffffffffffff"},
	} {
		for _, width := range []string{"integer", "bigint"} {
			for _, input := range []string{"-1", "NULL"} {
				t.Run(tc.name+"/"+width+"/"+input, func(t *testing.T) {
					b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
					column := catalog.Column{Name: "v", Usage: b.Type(sqltype.Text).Usage()}
					parseDefault := func(expression string) parser.Expr {
						t.Helper()
						statements, err := parser.Parse("CREATE TABLE d(v TEXT DEFAULT (" + expression + "))")
						if err != nil {
							t.Fatal(err)
						}
						return statements[0].(*parser.CreateTable).Elements[0].Column.Constraints[0].Default
					}
					original, err := b.BindDefault(parseDefault(tc.name+"("+input+"::"+width+")"), column)
					if err != nil {
						t.Fatal(err)
					}
					canonical, err := stored.DefaultSQL(ctx, e.CatalogRead(), original)
					if err != nil {
						t.Fatal(err)
					}
					replayed, err := b.BindDefault(parseDefault(canonical), column)
					if err != nil {
						t.Fatal(canonical, err)
					}
					for _, node := range []expr.Expr{original, replayed} {
						selected := node.(functions.String)
						id, want := sqltype.Int4, tc.int4
						if width == "bigint" {
							id, want = sqltype.Int8, tc.int8
						}
						if selected.Plan.ArgumentUsage(0).Type.ID() != id {
							t.Fatal("replay changed selected width", canonical, selected.Plan.ArgumentUsage(0))
						}
						value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
						if err != nil || value.Type() != b.Type(sqltype.Text) || value.IsNull() != (input == "NULL") {
							t.Fatal(value, err)
						}
						if input != "NULL" {
							got, err := value.Text()
							if err != nil || got != want {
								t.Fatal(got, want, err)
							}
						}
					}
				})
			}
		}
	}
}

func TestStringNumericBaseMD5InternalCharAdmission(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, null := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "a"}}
		if null {
			literal.Value.Kind = parser.NullValue
		}
		cast := parser.CastExpr{Child: literal, TypeName: parser.TypeName{Name: "char", Quoted: true}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "md5"}}}, Args: []parser.Expr{cast}}
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(null, err)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || value.Type() != b.Type(sqltype.Text) || value.IsNull() != null {
			t.Fatal(value, err)
		}
		if !null {
			digest, err := value.Text()
			if err != nil || digest != "0cc175b9c0f1b6a831c399e269772661" {
				t.Fatal(digest, err)
			}
		}
		// Canonical view definitions require direct CHAR string/NULL casts.
		// Both operand roles must preserve the same typed literal value.
		for _, textual := range []bool{false, true} {
			operand, err := b.StringOperand(cast, textual)
			if err != nil {
				t.Fatal("literal CHAR operand", textual, err)
			}
			value, err := operand.Node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil || value.Type() != b.Type(sqltype.Char) || value.IsNull() != null {
				t.Fatal("literal CHAR value", textual, value, err)
			}
			if !null {
				got, err := value.Type().Output(ctx, value)
				if err != nil || got != "a" {
					t.Fatal(got, err)
				}
			}
		}
		direct, err := b.Bind(cast, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || direct.ResultUsage().Type != b.Type(sqltype.Char) {
			t.Fatal("direct literal CHAR cast", direct, err)
		}
		usage, err := core.EngineResolveType(e, cast.TypeName, binderOperation(b))
		if err != nil || usage.Type != b.Type(sqltype.Char) {
			t.Fatal("ordinary CHAR admission", usage, err)
		}
	}
	for _, established := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		char := b.Type(sqltype.Char).Usage()
		if established {
			b.Params.Parameters[1] = char
		}
		cast := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "char", Quoted: true}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "md5"}}}, Args: []parser.Expr{cast}}
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("CHAR parameter annotation", established, err)
		}
		if node.ResultUsage().Type != b.Type(sqltype.Text) || b.Params.Parameters[1] != char {
			t.Fatal("lost annotated CHAR parameter contract", established, node.ResultUsage(), b.Params.Parameters[1])
		}
	}
}
