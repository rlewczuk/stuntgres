package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryHashCarrierEvaluationAndRetag(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	replacement := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, d := range sqltype.Builtins() {
		replacement[d.ID()] = d
	}
	d := types[sqltype.Bytea]
	value, _ := sqltype.Binary(d, "abc")
	for _, name := range []string{"crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512"} {
		t.Run(name, func(t *testing.T) {
			id := sqltype.Bytea
			if name == "crc32" || name == "crc32c" {
				id = sqltype.Int8
			}
			if name == "md5" {
				id = sqltype.Text
			}
			u := types[id].Usage()
			p, err := sqltype.BindBinary(name, u, d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			var order []int
			expr := binaryExpression{expressionInfo: expressionInfo{usage: u}, plan: p, args: []BoundExpr{mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, value: value, order: &order, index: 0}}}
			got, err := expr.Eval(&EvalContext{Context: context.Background()})
			if err != nil || !reflect.DeepEqual(order, []int{0}) || got.Type() != u.Type {
				t.Fatal(got, order, err)
			}
			failure := errors.New("hash child failure")
			expr.args[0] = mathObservedArgument{expressionInfo: expressionInfo{usage: d.Usage()}, err: failure, order: &order, index: 0}
			order = nil
			if _, err := expr.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0}) {
				t.Fatal(order, err)
			}
			// Stored replay changes descriptor owners but preserves payload/properties
			// and rebuilds the same BinaryPlan rather than another expression family.
			expr.args[0] = literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: value}
			rebound, changed, err := retagBoundTypes(New().CatalogRead(), expr, func(old *sqltype.Descriptor) *sqltype.Descriptor { return replacement[old.ID()] })
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			next, ok := rebound.(binaryExpression)
			if !ok || next.plan.Name() != name || next.plan.ResultUsage().Type != replacement[id] || next.args[0].ResultUsage().Type != replacement[sqltype.Bytea] {
				t.Fatal(rebound)
			}
			result, err := next.Eval(&EvalContext{Context: context.Background()})
			if err != nil {
				t.Fatal(err)
			}
			retagged, err := got.Retag(replacement[id])
			if err != nil || result != retagged {
				t.Fatal(result, retagged, err)
			}
			if expr.plan.ResultUsage().Type != types[id] || expr.args[0].ResultUsage().Type != d {
				t.Fatal("source carrier mutated")
			}
		})
	}
}

func TestBinaryHashOccurrenceBinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	call := func(name string, arg parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: parser.Position{Offset: 8, Character: 8}}}}, Args: []parser.Expr{arg}, Pos: parser.Position{Offset: 8, Character: 8}}
	}
	parameter := parser.ParameterExpr{Number: 1, Pos: parser.Position{Offset: 15, Character: 15}}
	sha, err := b.bind(call("sha256", parameter), expressionPolicy{context: projectionContext})
	if err != nil || sha.ResultUsage().Type.ID() != sqltype.Bytea {
		t.Fatal(sha, err)
	}
	md5, err := b.bind(call("md5", parameter), expressionPolicy{context: projectionContext})
	if err != nil || md5.ResultUsage().Type.ID() != sqltype.Text {
		t.Fatal(md5, err)
	}
	fresh := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	selected, err := fresh.bind(call("md5", parameter), expressionPolicy{context: projectionContext})
	if err != nil || selected.ResultUsage().Type.ID() != sqltype.Text || fresh.parameters[1].Type.ID() != sqltype.Text {
		t.Fatal(selected, err, "unresolved MD5 did not select TEXT")
	}
}
