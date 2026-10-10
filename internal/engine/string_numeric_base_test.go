package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringNumericBaseBindingAndRebinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	call := func(name string, arg parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{arg}, Pos: parser.Position{Character: 8, Offset: 8}}
	}
	parameter := parser.ParameterExpr{Number: 1}
	for _, name := range []string{"to_bin", "to_oct", "to_hex"} {
		_, err := b.bind(call(name, parameter), expressionPolicy{context: projectionContext})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42725" || diagnostic.Position != "8" || diagnostic.Detail != "" || diagnostic.Hint != "Could not choose a best candidate function. You might need to add explicit type casts." {
			t.Fatal(name, err)
		}
		if b.parameters[1].Type != nil {
			t.Fatal("ambiguous call fixed parameter")
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Int8} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.parameters[1] = b.typ(id).Usage()
		node, err := b.bind(call("to_hex", parameter), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		selected, ok := node.(stringExpression)
		if !ok || selected.plan.ArgumentUsage(0).Type.ID() != id || selected.ResultUsage().Type.ID() != sqltype.Text || selected.Properties().Volatility != Immutable {
			t.Fatal(node)
		}
		v, err := sqltype.Integer(b.typ(id), -1)
		if err != nil {
			t.Fatal(err)
		}
		selected.args[0] = literalExpression{expressionInfo: expressionInfo{usage: b.typ(id).Usage()}, value: v}
		replacements := make(map[sqltype.ID]*sqltype.Descriptor)
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		rebound, changed, err := retagBoundTypes(New().CatalogRead(), selected, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		next := rebound.(stringExpression)
		if next.plan.ArgumentUsage(0).Type != replacements[id] || next.ResultUsage().Type != replacements[sqltype.Text] {
			t.Fatal(next)
		}
		got, err := next.Eval(&EvalContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		text, err := got.Text()
		want := "ffffffff"
		if id == sqltype.Int8 {
			want = "ffffffffffffffff"
		}
		if err != nil || text != want {
			t.Fatal(text, err)
		}
		if selected.plan.ArgumentUsage(0).Type != b.typ(id) {
			t.Fatal("source provider mutated")
		}
	}
}

func TestStringNumericBaseMD5Rebinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	u := b.typ(sqltype.Text).Usage()
	value := sqltype.String(u.Type, "abc")
	arg := literalExpression{expressionInfo: expressionInfo{usage: u}, value: value}
	node, err := b.stringNode("md5", u, parser.Position{}, []BoundExpr{arg}, [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	replacements := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := rebound.(stringExpression)
	got, err := next.Eval(&EvalContext{Context: context.Background()})
	if err != nil || got.Type() != replacements[sqltype.Text] {
		t.Fatal(got, err)
	}
	text, err := got.Text()
	if err != nil || text != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatal(text, err)
	}
}

func TestStringNumericBaseStoredWidthReplay(t *testing.T) {
	ctx := context.Background()
	e := New()
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
					column := ColumnDescriptor{Name: "v", Usage: b.typ(sqltype.Text).Usage()}
					parseDefault := func(expression string) parser.Expr {
						t.Helper()
						statements, err := parser.Parse("CREATE TABLE d(v TEXT DEFAULT (" + expression + "))")
						if err != nil {
							t.Fatal(err)
						}
						return statements[0].(*parser.CreateTable).Elements[0].Column.Constraints[0].Default
					}
					original, err := b.bindDefault(parseDefault(tc.name+"("+input+"::"+width+")"), column)
					if err != nil {
						t.Fatal(err)
					}
					canonical, err := defaultSQL(ctx, e.CatalogRead(), original)
					if err != nil {
						t.Fatal(err)
					}
					replayed, err := b.bindDefault(parseDefault(canonical), column)
					if err != nil {
						t.Fatal(canonical, err)
					}
					for _, node := range []BoundExpr{original, replayed} {
						selected := node.(stringExpression)
						id, want := sqltype.Int4, tc.int4
						if width == "bigint" {
							id, want = sqltype.Int8, tc.int8
						}
						if selected.plan.ArgumentUsage(0).Type.ID() != id {
							t.Fatal("replay changed selected width", canonical, selected.plan.ArgumentUsage(0))
						}
						value, err := node.Eval(&EvalContext{Context: ctx})
						if err != nil || value.Type() != b.typ(sqltype.Text) || value.IsNull() != (input == "NULL") {
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
	e := New()
	for _, null := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "a"}}
		if null {
			literal.Value.Kind = parser.NullValue
		}
		cast := parser.CastExpr{Child: literal, TypeName: parser.TypeName{Name: "char", Quoted: true}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "md5"}}}, Args: []parser.Expr{cast}}
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(null, err)
		}
		value, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil || value.Type() != b.typ(sqltype.Text) || value.IsNull() != null {
			t.Fatal(value, err)
		}
		if !null {
			got, err := value.Text()
			if err != nil || got != "0cc175b9c0f1b6a831c399e269772661" {
				t.Fatal(got, err)
			}
		}
		for _, textual := range []bool{false, true} {
			_, err := b.stringOperand(cast, textual)
			if textual && err != nil {
				t.Fatal("textual role rejected CHAR", err)
			}
			if !textual {
				state(t, err, "0A000")
			}
		}
		_, err = b.bind(cast, expressionPolicy{context: projectionContext})
		state(t, err, "0A000")
		_, err = e.resolveType(cast.TypeName, b.op)
		state(t, err, "0A000")
		if _, err := sqltype.PlanCoercion(b.typ(sqltype.Char).Usage(), b.typ(sqltype.Text).Usage(), sqltype.Explicit); err == nil {
			t.Fatal("string role broadened general CHAR cast")
		}
	}
	for _, established := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		char := b.typ(sqltype.Char).Usage()
		if established {
			b.parameters[1] = char
		}
		cast := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "char", Quoted: true}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "md5"}}}, Args: []parser.Expr{cast}}
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal("CHAR parameter annotation", established, err)
		}
		if node.ResultUsage().Type != b.typ(sqltype.Text) || b.parameters[1] != char {
			t.Fatal("lost annotated CHAR parameter contract", established, node.ResultUsage(), b.parameters[1])
		}
		if _, err := sqltype.PlanCoercion(char, char, sqltype.Explicit); err == nil {
			t.Fatal("textual input admission broadened general CHAR identity casts")
		}
	}
}
