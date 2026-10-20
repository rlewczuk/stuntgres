package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExpressionArithmeticInferenceAndDependencies(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(s SMALLINT,i INT,b BIGINT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, tc := range []struct {
		q       string
		slot    sqltype.ID
		results []sqltype.ID
	}{{"SELECT i+$1,b+$1 FROM t", sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Int8}}, {"SELECT b+$1,i+$1 FROM t", sqltype.Int8, []sqltype.ID{sqltype.Int8, sqltype.Int8}}, {"SELECT s+$1,i+$1 FROM t", sqltype.Int2, []sqltype.ID{sqltype.Int2, sqltype.Int4}}, {"SELECT s%$1,b%$1 FROM t", sqltype.Int2, []sqltype.ID{sqltype.Int2, sqltype.Int8}}, {"SELECT i+$1,s+$1 FROM t", sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Int4}}, {"SELECT b+$1,s+$1 FROM t", sqltype.Int8, []sqltype.ID{sqltype.Int8, sqltype.Int8}}, {"SELECT $1+7,$1+2147483648 FROM t", sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Int8}}} {
		ast := statement(t, tc.q)
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if bound.parameters.Count != 1 || bound.parameters.Types[0].Type.ID() != tc.slot {
			t.Fatal(bound.parameters)
		}
		project := bound.query.Root.(*projectNode)
		for i, n := range project.Expressions {
			if n.ResultUsage().Type.ID() != tc.results[i] || !n.Properties().ParameterDependent || n.Properties().Volatility != expr.Immutable {
				t.Fatal(n)
			}
			if tc.q == "SELECT s+$1,i+$1 FROM t" && i == 1 {
				a := n.(arithmeticExpression)
				if a.Left.ResultUsage().Type.ID() != sqltype.Int4 || a.Right.ResultUsage().Type.ID() != sqltype.Int2 {
					t.Fatal("mixed addition lost actual operand widths", a)
				}
			}
			if tc.q == "SELECT s%$1,b%$1 FROM t" && i == 1 {
				a := n.(arithmeticExpression)
				if a.Left.ResultUsage().Type.ID() != sqltype.Int8 || a.Right.ResultUsage().Type.ID() != sqltype.Int8 {
					t.Fatal("mixed modulo lost homogeneous signature", a)
				}
				if _, ok := a.Right.(coercionExpression); !ok {
					t.Fatal("mixed modulo omitted implicit widening", a.Right)
				}
			}
		}
		if tc.q != "SELECT $1+7,$1+2147483648 FROM t" {
			for _, n := range project.Expressions {
				if !n.Properties().RowDependent {
					t.Fatal("lost recursive row dependence")
				}
			}
			columns := 0
			for _, d := range bound.dependencies {
				if d.Ref.Address.SubID != 0 {
					columns++
				}
			}
			if columns != 2 {
				t.Fatal("lost recursive column dependencies", bound.dependencies)
			}
		}
	}
}
