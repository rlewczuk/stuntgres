package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestGeometryAlignmentProviderLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right sqltype.ID
		a, b        string
	}{
		{"?-", 0, sqltype.Line, "{0,1,0}", ""}, {"?|", 0, sqltype.Line, "{1,0,0}", ""},
		{"?-", 0, sqltype.Lseg, "[(0,0),(1,0)]", ""}, {"?|", 0, sqltype.Lseg, "[(0,0),(0,1)]", ""},
		{"?||", sqltype.Line, sqltype.Line, "{0,1,0}", "{0,1,7}"},
		{"?-|", sqltype.Line, sqltype.Line, "{0,1,0}", "{1,0,7}"},
		{"?||", sqltype.Lseg, sqltype.Lseg, "[(0,0),(0,1)]", "[(2,0),(2,1)]"},
		{"?-|", sqltype.Lseg, sqltype.Lseg, "[(0,0),(0,1)]", "[(0,0),(1,0)]"},
	} {
		n, c, order := relationProviderUnit(t, tc.name, tc.left, tc.right, tc.a, tc.b)
		fresh, _, _ := relationProviderUnit(t, tc.name, tc.left, tc.right, tc.a, tc.b)
		count := n.Plan.Operation().Arity()
		wantOrder := []int{0}
		if count == 2 {
			wantOrder = []int{0, 1}
		}
		v, err := n.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Boolean()
		if err != nil || !got || v.Type() != n.Usage.Type || !reflect.DeepEqual(*order, wantOrder) {
			t.Fatal("evaluation", tc, v, err, *order)
		}
		if len(n.Children()) != count || n.ValidateStoredProvider() != nil {
			t.Fatal("active children", tc)
		}
		capture, ok, err := n.Capture(expr.CaptureOperator)
		if err != nil || !ok || capture.Count != count || !reflect.DeepEqual(capture.Inline, n.Args) {
			t.Fatal("capture", tc, capture, err)
		}
		if _, ok, err := n.Capture(expr.CaptureFunction); ok || err != nil {
			t.Fatal("call admission", tc)
		}
		rendered, err := n.Render()
		wantForm := expr.FormInfix
		if count == 1 {
			wantForm = expr.FormTemplate
		}
		if err != nil || rendered.Form != wantForm || rendered.Namespace != "pg_catalog" || rendered.Replay.Validate == nil || rendered.Replay.Validate() != nil {
			t.Fatal("rendering", tc, rendered, err)
		}
		if count == 1 && (len(rendered.Pieces) != 3 || len(rendered.Replay.Pieces) != 5) {
			t.Fatal("prefix template", rendered)
		}
		for i, arg := range rendered.Args {
			if arg.Cast != n.Args[i].ResultUsage() || arg.Transport != expr.TransportCast || arg.TransportType != n.Args[i].ResultUsage() {
				t.Fatal("formal transport", tc, i)
			}
		}
		rebuilt, err := n.Rebuild(fresh.Info, fresh.Children())
		if err != nil || rebuilt.(GeometryRelation).Validate() != nil || rebuilt.(GeometryRelation).Plan == n.Plan {
			t.Fatal("reownership", tc, err)
		}
		if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
			t.Fatal("foreign children", tc)
		}
		if _, err := n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("missing children", tc)
		}
		if _, err := n.Substitute(n.Info, nil); err == nil {
			t.Fatal("missing substitution", tc)
		}
		sub, err := n.Substitute(n.Info, n.Children())
		if err != nil || sub.(GeometryRelation).Validate() != nil {
			t.Fatal("substitution", tc, err)
		}
		same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
		if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
			t.Fatal("group authority", tc)
		}
		qualified := n
		qualified.Namespace = "alignment_namespace"
		if n.SameGroup(qualified, same, false) {
			t.Fatal("namespace identity", tc)
		}
		if r, err := qualified.Render(); err != nil || r.Namespace != "alignment_namespace" || count == 1 && !reflect.DeepEqual(r.Pieces, r.Replay.Pieces) {
			t.Fatal("qualified replay", tc, r, err)
		}
		bad := n
		bad.Args[0] = nil
		if bad.Validate() == nil {
			t.Fatal("missing operand", tc)
		}
		if count == 1 {
			bad = n
			bad.Args[1] = n.Args[0]
			if bad.Validate() == nil {
				t.Fatal("extra inactive operand", tc)
			}
			if _, err := n.Rebuild(n.Info, bad.Args[:]); err == nil {
				t.Fatal("extra rebuild operand", tc)
			}
		}
		arg := n.Args[0].(stringUnitArgument)
		arg.value = sqltype.Null(arg.Usage.Type)
		n.Args[0] = arg
		if count == 2 {
			right := n.Args[1].(stringUnitArgument)
			right.err = errors.New("alignment child failure")
			n.Args[1] = right
			if _, err := n.Eval(c); !errors.Is(err, right.err) {
				t.Fatal("NULL hid child error", tc, err)
			}
		} else if v, err := n.Eval(c); err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
			t.Fatal("runtime NULL", tc, v, err)
		}
		arg.literal = true
		n.Args[0] = arg
		*order = nil
		if v, err := n.Eval(c); err != nil || !v.IsNull() || len(*order) != 0 {
			t.Fatal("literal NULL", tc, v, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = ctx
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation", tc, err)
		}
	}
}
