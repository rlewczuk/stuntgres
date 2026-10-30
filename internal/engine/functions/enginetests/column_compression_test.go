package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestColumnCompressionProtocol(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, operand := range []string{"$1::text", "$1::integer", "'short'", "NULL::text"} {
		t.Run(operand, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "pg_catalog.pg_column_compression("+operand+")")
			n := node.(functions.ColumnCompression)
			declaration, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || declaration == nil || declaration.Builtin == nil {
				t.Fatal("selected declaration", declaration, err)
			}
			d := declaration.Builtin
			if d.Identity.ID != 2121 || d.Identity.Namespace != 11 || d.Identity.Name != "pg_column_compression" || d.Identity.Signature != "2276" || d.Result != 25 || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet || d.Providers != functions.CatalogRoutineProvider {
				t.Fatal("released identity and actual provider", d)
			}
			if declaration.Display.Language != catalog.InternalRoutineLanguage || declaration.Display.Source != "pg_column_compression" || declaration.Cost != 1 {
				t.Fatal("released display and cost", declaration.Display, declaration.Cost)
			}
			if n.Validate() != nil || n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.ResultUsage().Type.ID() != sqltype.Text {
				t.Fatal("immutable typed protocol", n)
			}
			if len(n.Children()) != 1 || !reflect.DeepEqual(n.Children()[0], n.Arg) {
				t.Fatal("single concrete operand", n.Children())
			}
			original := n
			info := n.Info
			info.Occurrence++
			rebuilt, err := n.Rebuild(info, n.Children())
			if err != nil {
				t.Fatal(err)
			}
			next := rebuilt.(functions.ColumnCompression)
			if next.Info != info || next.RoutineReference != n.RoutineReference || !reflect.DeepEqual(next.Arg, n.Arg) || !reflect.DeepEqual(n, original) {
				t.Fatal("reconstruction changed owned recipe or routine authority")
			}
			if !n.SameGroup(next, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("reconstructed grouping")
			}
			for _, children := range [][]expr.Expr{nil, {n.Arg, n.Arg}, {nil}} {
				if _, err := n.Rebuild(n.Info, children); err == nil {
					t.Fatal("malformed children accepted", children)
				}
			}
			bad := n
			bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Int4)
			if bad.Validate() == nil {
				t.Fatal("non-TEXT result accepted")
			}
			if _, err := n.Rebuild(bad.Info, n.Children()); err == nil {
				t.Fatal("non-TEXT reconstruction accepted")
			}
			bad = n
			bad.Arg = nil
			if bad.Validate() == nil {
				t.Fatal("missing operand accepted")
			}
			if _, err := bad.Render(); err == nil {
				t.Fatal("invalid recipe rendered")
			}
			render, err := n.Render()
			if err != nil || render.Name != "pg_column_compression" || render.Form != expr.FormCall || len(render.Inputs) != 1 || len(render.Args) != 1 || !reflect.DeepEqual(render.Inputs[0], n.Arg) {
				t.Fatal("ordinary renderer", render, err)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 1 || capture.Check(n, 1) != nil {
				t.Fatal("function capture", capture, err)
			}
			if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
				t.Fatal("operator capture", ok, err)
			}
			retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed || !reflect.DeepEqual(retagged, n) {
				t.Fatal("identity retag changed recipe/provenance", retagged, err)
			}
			if operand == "$1::text" || operand == "$1::integer" {
				typ := binder.Params.Parameters[1].Type
				if n.Arg.ResultUsage().Type != typ {
					t.Fatal("any formal coerced concrete input", n.Arg.ResultUsage(), typ)
				}
				evaluation.Parameters = []sqltype.Value{sqltype.Null(typ)}
				value, err := n.Eval(&evaluation)
				if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
					t.Fatal("strict typed NULL", value, err)
				}
				if typ.ID() == sqltype.Text {
					evaluation.Parameters[0] = sqltype.String(typ, "short")
				} else {
					evaluation.Parameters[0], err = sqltype.Integer(typ, 7)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			value, err := n.Eval(&evaluation)
			if operand == "NULL::text" {
				if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
					t.Fatal("strict literal NULL", value, err)
				}
			} else {
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "0A000", Message: "pg_column_compression physical TOAST compression inspection is unsupported in embedded mode"})
				if value.Type() != nil {
					t.Fatal("failed inspection manufactured a value", value)
				}
				planned, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, n, true)
				if err != nil || reflect.TypeOf(planned) != reflect.TypeOf(n) {
					t.Fatal("preflight demanded physical inspection", planned, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			evaluation.Context = ctx
			if _, err := n.Eval(&evaluation); !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary cancellation", err)
			}
			if !reflect.DeepEqual(n, original) {
				t.Fatal("evaluation mutated immutable recipe")
			}
		})
	}
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_column_compression(1/0)")
	child := node.(functions.ColumnCompression).Arg
	_, expected := child.Eval(&evaluation)
	_, actual := node.Eval(&evaluation)
	if expected == nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("parent replaced child diagnostic", actual, expected)
	}
	nullCountUnitDiagnostic(t, actual, sqlstate.Error{Code: "22012", Message: "division by zero"})
}
