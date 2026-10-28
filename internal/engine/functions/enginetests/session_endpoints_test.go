package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestSessionEndpointLeafOwnership(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, tc := range []struct {
		name string
		oid  uint32
		id   sqltype.ID
	}{
		{"inet_client_addr", 2196, sqltype.Inet},
		{"inet_client_port", 2197, sqltype.Int4},
		{"inet_server_addr", 2198, sqltype.Inet},
		{"inet_server_port", 2199, sqltype.Int4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.name+"()")
			n := node.(functions.SessionEndpoint)
			declaration, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || declaration == nil || declaration.Builtin == nil || uint32(declaration.Address().ObjectID) != tc.oid || declaration.Strict || declaration.Volatility != expr.Stable || declaration.Builtin.Parallel != 'r' || declaration.ReturnsSet() || n.ResultUsage().Type.ID() != tc.id {
				t.Fatal("routine declaration", declaration, err)
			}
			expectedType, found := e.CatalogRead().LookupType(tc.id)
			if !found || n.Usage.Type != expectedType || uint32(declaration.Result) != uint32(tc.id) {
				t.Fatal("catalog-owned result descriptor", n.Usage.Type, expectedType)
			}
			if n.Kind != functions.SessionEndpointKind(i) || n.Validate() != nil || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || !n.LooksLikeFunction() || n.ClosedDefault(func(expr.Expr) bool { return true }) || len(n.Children()) != 0 || n.ValidateStoredProvider() != nil {
				t.Fatal("leaf protocol", n)
			}
			if admitted, set := n.SourceAdmission(); !admitted || set {
				t.Fatal("scalar admission", admitted, set)
			}
			before := n
			value, err := n.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
				t.Fatal("typed endpoint absence", value, err)
			}
			info := n.Info
			info.Occurrence++
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Substitute, n.Rebuild} {
				next, err := rebuild(info, nil)
				if err != nil {
					t.Fatal(err)
				}
				rebuilt := next.(functions.SessionEndpoint)
				descriptor, err := exec.ExpressionRoutineDescriptor(rebuilt)
				if err != nil || descriptor != declaration || rebuilt.Info != info || rebuilt.Kind != n.Kind || rebuilt.RoutineReference != n.RoutineReference || rebuilt.Usage.Type != n.Usage.Type || n != before {
					t.Fatal("reconstruction ownership", rebuilt, err)
				}
				if _, err := rebuild(info, []expr.Expr{n}); err == nil {
					t.Fatal("leaf accepted children")
				}
			}
			retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed || !reflect.DeepEqual(retagged, n) {
				t.Fatal("immutable recipe retag", retagged, err)
			}
			rendered, err := n.Render()
			if err != nil || rendered.Tree != "SessionEndpoint" || rendered.Form != expr.FormCall || rendered.Name != tc.name || len(rendered.Args) != 0 || len(rendered.Inputs) != 0 {
				t.Fatal("render", rendered, err)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 0 {
				t.Fatal("function capture", capture, ok, err)
			}
			if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
				t.Fatal("nonfunction capture", ok, err)
			}
			if !n.SameGroup(n, nil, false) {
				t.Fatal("same kind grouping")
			}
			other := n
			other.Kind = functions.SessionEndpointKind((i + 2) % 4)
			if n.SameGroup(other, nil, false) {
				t.Fatal("equal NULL types merged distinct endpoints")
			}
			other.Kind = functions.SessionEndpointKind((i + 1) % 4)
			if n.SameGroup(other, nil, false) {
				t.Fatal("distinct endpoint kinds grouped")
			}
			bad := n
			bad.Kind = 255
			if bad.Validate() == nil || bad.ValidateStoredProvider() == nil {
				t.Fatal("invalid kind accepted")
			}
			if _, err := bad.Render(); err == nil {
				t.Fatal("invalid kind rendered")
			}
			if _, err := bad.Eval(&evaluation); err == nil {
				t.Fatal("invalid kind evaluated")
			}
			bad = n
			bad.Usage.Type = nil
			if bad.Validate() == nil {
				t.Fatal("nil type accepted")
			}
			if _, err := n.Rebuild(bad.Info, nil); err == nil {
				t.Fatal("nil reconstruction type accepted")
			}
			bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
			if bad.Validate() == nil {
				t.Fatal("wrong type accepted")
			}
			if _, err := n.Rebuild(bad.Info, nil); err == nil {
				t.Fatal("wrong reconstruction type accepted")
			}
			cursor, err := n.OpenSource(&evaluation)
			if err != nil {
				t.Fatal(err)
			}
			first, present, err := cursor.Next(context.Background())
			if err != nil || !present || !first.IsNull() || first.Type() != n.Usage.Type {
				t.Fatal("scalar NULL row", first, present, err)
			}
			last, present, err := cursor.Next(context.Background())
			if err != nil || present || !last.IsNull() || last.Type() != n.Usage.Type {
				t.Fatal("typed scalar EOF", last, present, err)
			}
			cursor.Close()
			if cursor.State != nil || cursor.Values != nil || cursor.Value.Type() != nil || !cursor.Exhausted {
				t.Fatal("source retained invocation state", cursor)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			evaluation.Context = ctx
			if value, err := n.Eval(&evaluation); !errors.Is(err, context.Canceled) || value.Type() != nil {
				t.Fatal("ordinary cancellation", value, err)
			}
			if _, err := n.OpenSource(&evaluation); !errors.Is(err, context.Canceled) {
				t.Fatal("source cancellation", err)
			}
			evaluation.Context = nil
			if _, err := n.Eval(&evaluation); err == nil || err.Error() != "stuntgres: missing expression context" {
				t.Fatal("missing context invariant", err)
			}
			if n != before {
				t.Fatal("evaluation mutated recipe")
			}
		})
	}
}
