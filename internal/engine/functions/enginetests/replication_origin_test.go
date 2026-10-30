package enginetests_test

import (
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestReplicationOriginStateBindingProtocol(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	calls := []string{"pg_replication_origin_create(NULL::text)", "pg_replication_origin_drop(NULL::text)", "pg_replication_origin_oid(NULL::text)", "pg_replication_origin_advance(NULL::text,'0/1')", "pg_replication_origin_progress(NULL::text,true)"}
	for i, call := range calls {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.ReplicationOrigin)
		before := n
		result := []sqltype.ID{sqltype.Oid, sqltype.Void, sqltype.Oid, sqltype.Void, sqltype.PgLSN}[i]
		oid := []ids.OID{6003, 6004, 6005, 6012, 6013}[i]
		procedure, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || procedure.Address().ObjectID != oid || n.Kind != functions.ReplicationOriginKind(i) || n.ResultUsage().Type.ID() != result || n.Validate() != nil || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep {
			t.Fatal(n, procedure, err)
		}
		volatility := expr.Volatile
		if i == 2 {
			volatility = expr.Stable
		}
		if n.IntrinsicVolatility() != volatility {
			t.Fatal("volatility", n)
		}
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal("strict typed NULL", value, err)
		}
		children := n.Children()
		saved := append([]expr.Expr(nil), children...)
		children[0] = nil
		if n.Args[0] == nil {
			t.Fatal("children borrowed recipe backing")
		}
		rebuilt, err := n.Rebuild(n.Info, saved)
		if err != nil {
			t.Fatal(err)
		}
		next := rebuilt.(functions.ReplicationOrigin)
		if next.Args != n.Args || next.RoutineReference != n.RoutineReference || next.Occurrence != n.Occurrence || !reflect.DeepEqual(n, before) {
			t.Fatal("reconstruction changed authority or original")
		}
		rendering, err := next.Render()
		if err != nil || rendering.Name != n.Kind.Name() || len(rendering.Args) != int(n.Arity) {
			t.Fatal(rendering, err)
		}
		for _, arg := range rendering.Args {
			if arg.Label != "" {
				t.Fatal("invented callable names")
			}
		}
		capture, matched, err := next.Capture(expr.CaptureFunction)
		if err != nil || !matched || capture.Count != int(n.Arity) || !capture.Indexed {
			t.Fatal(capture, err)
		}
		for j := range int(n.Arity) {
			child, err := next.CaptureOperand(j)
			if err != nil || child != saved[j] {
				t.Fatal("capture ownership", child, err)
			}
		}
		if _, err := next.CaptureOperand(-1); err == nil {
			t.Fatal("negative operand admitted")
		}
		if _, err := next.CaptureOperand(int(n.Arity)); err == nil {
			t.Fatal("extra operand admitted")
		}
		if _, err := n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("missing children admitted")
		}
		bad := n
		bad.Kind = 255
		if bad.Validate() == nil {
			t.Fatal("invalid kind admitted")
		}
		bad = n
		bad.Arity++
		if bad.Validate() == nil {
			t.Fatal("invalid arity admitted")
		}
		bad = n
		bad.Args[0] = nil
		if bad.Validate() == nil {
			t.Fatal("missing operand admitted")
		}
		if n.Arity == 1 {
			bad = n
			bad.Args[1] = n.Args[0]
			if bad.Validate() == nil {
				t.Fatal("unused operand admitted")
			}
		}
		info := n.Info
		info.Usage = e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}).SQLType.Usage()
		if _, err := n.Rebuild(info, saved); err == nil {
			t.Fatal("wrong result admitted")
		}
		if !n.SameGroup(next, func(a, b expr.Expr) bool { return a == b }, false) {
			t.Fatal("same recipe group differs")
		}
	}
}
