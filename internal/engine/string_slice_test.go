package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringSliceFormalInference(t *testing.T) {
	for _, name := range []string{"left", "right", "substring", "substr"} {
		for _, arity := range []int{2, 3} {
			if arity == 3 && (name == "left" || name == "right") {
				continue
			}
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			args := make([]parser.Expr, arity)
			for i := range args {
				args[i] = parser.ParameterExpr{Number: i + 1}
			}
			if name == "substring" {
				args[1] = parser.CastExpr{Child: args[1], TypeName: parser.TypeName{Name: "int4"}, Syntax: parser.PostfixCast}
			}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
			var node BoundExpr
			var err error
			if name == "substring" || name == "substr" {
				node, err = b.bindBinaryFunction(raw, true)
			} else {
				node, err = b.bindStringFunction(raw, true)
			}
			if err != nil {
				t.Fatal(name, arity, err)
			}
			n := node.(functions.String[BoundExpr, *EvalContext, ExprProperties])
			for i := range arity {
				id := sqltype.Int4
				if i == 0 {
					id = sqltype.Text
				}
				if b.parameters[i+1].Type != b.typ(id) || n.Plan.ArgumentUsage(i).Type != b.typ(id) || n.Textual[i] != (i == 0) {
					t.Fatal(name, i, b.parameters, n.Plan.ArgumentUsage(i))
				}
			}
			if n.ResultUsage() != b.typ(sqltype.Text).Usage() || !n.Properties().ParameterDependent {
				t.Fatal(name, n)
			}
		}
	}
	for _, arity := range []int{2, 3} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		args := make([]parser.Expr, arity)
		for i := range args {
			args[i] = parser.ParameterExpr{Number: i + 1}
		}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "substring"}}}, Args: args}
		node, err := b.bindBinaryFunction(raw, true)
		if err != nil {
			t.Fatal(err)
		}
		if arity == 2 {
			if _, ok := node.(functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]); !ok {
				t.Fatal("unknown2 did not select POSIX", node)
			}
		} else if _, ok := node.(functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]); !ok {
			t.Fatal("unknown3 did not select SIMILAR", node)
		}
		for i := range arity {
			if b.parameters[i+1].Type != b.typ(sqltype.Text) {
				t.Fatal("regex parameter forced integer", arity, i, b.parameters)
			}
		}
	}
}

func TestStringSliceDependencyChildren(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, integer := b.typ(sqltype.Text), b.typ(sqltype.Int4)
	for _, name := range []string{"left", "right", "substring", "substr"} {
		arity := 2
		if name == "substring" || name == "substr" {
			arity = 3
		}
		args := make([]BoundExpr, arity)
		for i := range arity {
			typ := integer
			if i == 0 {
				typ = text
			}
			args[i] = columnExpression{expressionInfo: expressionInfo{usage: typ.Usage(), properties: ExprProperties{RowDependent: true}}, slot: i}
		}
		node, err := b.stringNode(name, text.Usage(), parser.Position{}, args, [4]bool{true}, parser.OrdinaryStringSyntax)
		if err != nil {
			t.Fatal(err)
		}
		var slots []int
		walkBoundExpression(node, func(child BoundExpr) {
			if col, ok := child.(columnExpression); ok {
				slots = append(slots, col.slot)
			}
		})
		want := []int{0, 1}
		if arity == 3 {
			want = append(want, 2)
		}
		if !reflect.DeepEqual(slots, want) || !node.Properties().RowDependent {
			t.Fatal(name, "lost dependency", slots, node.Properties())
		}
	}
}

func TestStringSliceReconstructionAndIdentity(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, integer := b.typ(sqltype.Text), b.typ(sqltype.Int4)
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	args := []BoundExpr{
		literalExpression{expressionInfo{usage: text.Usage()}, sqltype.String(text, "Aé🙂Z")},
		literalExpression{expressionInfo{usage: integer.Usage()}, two},
		literalExpression{expressionInfo{usage: integer.Usage()}, one},
	}
	ordinary, err := b.stringNode("substring", text.Usage(), parser.Position{}, args, [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	special, err := b.stringNode("substring", text.Usage(), parser.Position{}, args, [4]bool{true}, parser.SubstringStringSyntax)
	if err != nil || !b.sameGroupExpression(ordinary, special) || !b.sameGroupExpression(special, ordinary) {
		t.Fatal("presentation altered semantic identity", err)
	}
	fresh := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), special, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal("slice reconstruction", changed, err)
	}
	n := rebound.(functions.String[BoundExpr, *EvalContext, ExprProperties])
	if n.Syntax != parser.SubstringStringSyntax || n.ResultUsage() != fresh[sqltype.Text].Usage() {
		t.Fatal("lost reconstructed syntax/owner", n)
	}
	for i := range n.Args {
		id := sqltype.Int4
		if i == 0 {
			id = sqltype.Text
		}
		if n.Plan.ArgumentUsage(i).Type != fresh[id] || n.Args[i].ResultUsage().Type != fresh[id] {
			t.Fatal("retained foreign child/formal", i)
		}
	}
	got, err := n.Eval(&EvalContext{Context: context.Background()})
	s, _ := got.Text()
	if err != nil || s != "é" || got.Type() != fresh[sqltype.Text] {
		t.Fatal(s, err)
	}
	original := special.(functions.String[BoundExpr, *EvalContext, ExprProperties])
	if original.ResultUsage().Type != text || original.Plan.ArgumentUsage(1).Type != integer {
		t.Fatal("reconstruction mutated original")
	}
}

func TestStringSliceSimilarOrdinaryWrapper(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "substring"}}}, Args: []parser.Expr{literal("abc"), literal("%"), literal("xx")}}
	node, err := b.bindBinaryFunction(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Eval(&EvalContext{Context: context.Background()})
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character.", Context: `SQL function "substring" statement 1`}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal("ordinary literal wrapper diagnostic", err)
	}
}
