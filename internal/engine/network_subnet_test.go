package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestINETSubnetBinding(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"<<", "<<=", ">>", ">>=", "&&"} {
		e := New()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		node, err := b.Bind(bitOperatorRaw(op, networkOperatorTyped("inet", "192.168.1.5"), networkOperatorTyped("cidr", "192.168.1/24")), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(op, err)
		}
		if node.ResultUsage() != b.Type(sqltype.Bool).Usage() {
			t.Fatal(node.ResultUsage())
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := stored.DefaultSQL(ctx, e.CatalogRead(), node)
		if err != nil || !strings.Contains(text, op) {
			t.Fatal(text, err)
		}
		if !b.SameGroupExpression(node, node) {
			t.Fatal("group identity")
		}
		foreign := New()
		rebound, changed, err := stored.RetagTypes(foreign.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := foreign.CatalogRead().LookupType(d.ID())
			return next
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		after, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		want, err := value.Retag(rebound.ResultUsage().Type)
		if err != nil || after != want {
			t.Fatal(after, want, err)
		}
		if node.(functions.NetworkSubnet).Plan == rebound.(functions.NetworkSubnet).Plan {
			t.Fatal("stale subnet plan")
		}
		if _, err = stored.DefaultSQL(ctx, foreign.CatalogRead(), rebound); err != nil {
			t.Fatal(err)
		}
	}
}
func TestINETSubnetRoutineAuthority(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		op, name string
		oid      ids.OID
	}{{"<<", "network_sub", 927}, {"<<=", "network_subeq", 928}, {">>", "network_sup", 929}, {">>=", "network_supeq", 930}, {"&&", "network_overlap", 3551}} {
		e := New()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		node, err := b.Bind(bitOperatorRaw(tc.op, networkOperatorTyped("inet", "::1"), networkOperatorTyped("inet", "::/0")), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		d, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || d == nil || d.Builtin == nil {
			t.Fatal(d, err)
		}
		decl := d.Builtin
		if decl.Identity != (catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, "869,869"}) || decl.Result != 16 || !decl.Strict || decl.Providers != functions.OperatorRoutineProvider || len(d.Body) != 0 {
			t.Fatal(decl)
		}
		found := false
		for _, dep := range b.Dependencies {
			if dep.Ref.Address.ClassID == ids.ProcedureClassID {
				if dep.Ref.Address.ObjectID != tc.oid {
					t.Fatal("unexpected routine", dep)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("missing original routine dependency")
		}
		callBinder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		call, err := callBinder.Bind(parser.FunctionExpr{
			Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: tc.name}}},
			Args: []parser.Expr{networkOperatorTyped("inet", "::1"), networkOperatorTyped("cidr", "::/0")},
		}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(tc.name, err)
		}
		called, err := exec.ExpressionRoutineDescriptor(call)
		if err != nil || called == nil || called.Builtin != decl {
			t.Fatal("call did not select original operator routine", called, err)
		}
		operatorValue, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		callValue, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || callValue != operatorValue {
			t.Fatal(tc.name, callValue, operatorValue, err)
		}
	}
}
