package functions

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type accessExecutionOperand struct {
	expr.Expr
	order *[]int
	index int
}

func (n accessExecutionOperand) Eval(c expr.Context) (sqltype.Value, error) {
	*n.order = append(*n.order, n.index)
	return n.Expr.Eval(c)
}

type accessExecutionFailure struct {
	expr.Expr
	err error
}

func (n accessExecutionFailure) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, n.err
}

func TestAccessExecutionInquiryCarrier(t *testing.T) {
	accessInquiryCarrier(t, []AccessKind{AccessFunction, AccessLanguage, AccessForeignWrapper, AccessServer, AccessDatabase, AccessSchema, AccessType, AccessTablespace})
}

func TestAccessRelationsCarrier(t *testing.T) {
	accessInquiryCarrier(t, []AccessKind{AccessTable, AccessSequence, AccessColumnPrivilege, AccessAnyColumn})
}

func accessInquiryCarrier(t *testing.T, kinds []AccessKind) {
	for _, kind := range kinds {
		signatures := [][]sqltype.ID{{19, 25, 25}, {19, 26, 25}, {26, 25, 25}, {26, 26, 25}, {25, 25}, {26, 25}}
		if kind == AccessColumnPrivilege {
			signatures = [][]sqltype.ID{{19, 25, 25, 25}, {19, 25, 21, 25}, {19, 26, 25, 25}, {19, 26, 21, 25}, {26, 25, 25, 25}, {26, 25, 21, 25}, {26, 26, 25, 25}, {26, 26, 21, 25}, {25, 25, 25}, {25, 21, 25}, {26, 25, 25}, {26, 21, 25}}
		}
		for _, formals := range signatures {
			n := AccessInquiry{Kind: kind, Count: len(formals), Info: expr.Info{Usage: aclTestTypes[sqltype.Bool].Usage(), Property: expr.Properties{Volatility: expr.Stable}}}
			for i, id := range formals {
				if id == sqltype.Oid {
					n.Args[i] = aclTestOID(t, 42)
				} else if id == sqltype.Int2 {
					value, err := sqltype.Integer(aclTestTypes[id], 1)
					if err != nil {
						t.Fatal(err)
					}
					n.Args[i] = aclTestLiteral(value)
				} else {
					n.Args[i] = aclTestText(id, "value")
				}
			}
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			if n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren {
				t.Fatal("planning contract")
			}
			runtime := newACLTestRuntime()
			if v, err := n.Eval(runtime); err != nil || !aclTestTrue(v) || runtime.calls != 1 {
				t.Fatal("runtime dispatch", v, err)
			}
			var order []int
			ordered := n
			var want []int
			for i := range n.Count {
				ordered.Args[i] = accessExecutionOperand{n.Args[i], &order, i}
				want = append(want, i)
			}
			if _, err := ordered.Eval(runtime); err != nil || !reflect.DeepEqual(order, want) {
				t.Fatal("written child evaluation order", order, err)
			}
			order = nil
			ordered.Args[0] = accessExecutionOperand{aclTestLiteral(sqltype.Null(aclTestTypes[formals[0]])), &order, 0}
			if value, err := ordered.Eval(runtime); err != nil || !value.IsNull() || !reflect.DeepEqual(order, want) {
				t.Fatal("NULL short-circuited written siblings", order, err)
			}
			for i := range formals {
				null := n
				null.Args[i] = aclTestLiteral(sqltype.Null(aclTestTypes[formals[i]]))
				runtime.calls = 0
				if v, err := null.Eval(runtime); err != nil || !v.IsNull() || runtime.calls != 0 {
					t.Fatal("strict null reached authority", v, err)
				}
			}
			demanded := n
			demanded.Args[0] = aclTestLiteral(sqltype.Null(aclTestTypes[formals[0]]))
			failure := errors.New("demanded child")
			demanded.Args[n.Count-1] = accessExecutionFailure{n.Args[n.Count-1], failure}
			runtime.calls = 0
			if _, err := demanded.Eval(runtime); !errors.Is(err, failure) || runtime.calls != 0 {
				t.Fatal("strict NULL hid demanded child error or reached authority", err, runtime.calls)
			}
			rebuilt, err := n.Rebuild(n.Info, append([]expr.Expr(nil), n.Children()...))
			if err != nil {
				t.Fatal(err)
			}
			if !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
				t.Fatal("rebuild group")
			}
			other := n
			other.Kind = AccessMembership
			if n.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
				t.Fatal("kind lost from group")
			}
			rendered, err := n.Render()
			if err != nil || rendered.Name != n.Name() || len(rendered.Args) != n.Count {
				t.Fatal("render", rendered, err)
			}
			if _, ok, err := n.Capture(expr.CaptureFunction); err != nil || !ok {
				t.Fatal("capture", ok, err)
			}
			for i := range n.Count {
				operand, err := n.CaptureOperand(i)
				if err != nil || operand != n.Args[i] {
					t.Fatal("written operand", i, err)
				}
			}
			invalid := n
			invalid.Kind = AccessKind(255)
			if invalid.Validate() == nil {
				t.Fatal("invalid kind admitted")
			}
			invalid = n
			invalid.Args[n.Count-1] = aclTestOID(t, 1)
			if invalid.Validate() == nil {
				t.Fatal("invalid privilege formal admitted")
			}
		}
	}
}
