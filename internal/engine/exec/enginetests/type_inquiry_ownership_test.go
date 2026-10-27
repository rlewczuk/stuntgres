package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeExecutionOwnership(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_typeof"}}}, Args: []parser.Expr{parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: parser.TypeName{Name: "integer"}, Syntax: parser.PostfixCast}}}
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	info := expr.InfoOf(node)
	children := append([]expr.Expr(nil), node.(expr.Composite).Children()...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	if _, err := node.Eval(&canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("metadata lookup erased ordinary cancellation", err)
	}
	live := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err := node.Eval(&live)
	oid, oidErr := value.TypeOID()
	if err != nil || oidErr != nil || oid != uint32(sqltype.Int4) || value.IsNull() || value.Type() != node.ResultUsage().Type || value.Type().ID() != sqltype.Regtype {
		t.Fatal("typed NULL lost its exact nonnull REGTYPE identity", value, err, oidErr)
	}
	if expr.InfoOf(node) != info || !reflect.DeepEqual(node.(expr.Composite).Children(), children) {
		t.Fatal("canceled or live invocation mutated immutable recipe")
	}
	for _, run := range []func() (sqltype.Value, error){func() (sqltype.Value, error) { return live.FormatType(23, -1, false, value.Type()) }, func() (sqltype.Value, error) { return live.BaseType(23, value.Type()) }} {
		if _, err := run(); err == nil {
			t.Fatal("detached execution acquired catalog authority")
		}
	}
}
