package schemacmds_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type earthDiagnosticArgumentExpr struct {
	expr.Info
	failure error
}

func (n earthDiagnosticArgumentExpr) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, n.failure
}

func TestEarthCoordinateDiagnosticPresentation(t *testing.T) {
	e, ext, earth := earthUnitInstalled(t)
	before := e.CatalogRead()
	floating, _ := before.LookupType(sqltype.Float8)
	foreign, foreignExt, _ := earthUnitInstalled(t)
	for _, tc := range []struct {
		op      sqltype.EarthOperation
		slot    int
		input   float64
		message string
	}{
		{sqltype.EarthSecToGC, 3, math.SmallestNonzeroFloat64, "value out of range: underflow"},
		{sqltype.EarthLL, 5, math.Inf(1), "input is out of range"},
	} {
		t.Run(tc.op.Name(), func(t *testing.T) {
			captured := before.MustProcedure(ext.Members[tc.slot])
			args := make([]expr.Expr, tc.op.Arity())
			for i := range args {
				n := float64(0)
				if i == 0 {
					n = tc.input
				}
				value, err := sqltype.Float(floating, n)
				if err != nil {
					t.Fatal(err)
				}
				args[i] = plan.LiteralExpr{Info: expr.Info{Usage: floating.Usage()}, Value: value}
			}
			result := floating
			if tc.op == sqltype.EarthLL {
				result = earth.SQLType
			}
			kernel, err := functions.NewEarthCoordinate(expr.Info{Usage: result.Usage(), RoutineReference: captured}, tc.op, earth.SQLType, args)
			if err != nil {
				t.Fatal(err)
			}
			node := plan.InstalledEarthExpr{EarthCoordinate: kernel, Procedure: captured, Ref: e.Catalog.Domain.Ref(before.Epoch(), captured.Address())}
			assertBody := func(view catalog.Read, name string) {
				t.Helper()
				_, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}})
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "22003" || d.Message != tc.message || d.Context != `SQL function "`+name+`" statement 1` {
					t.Fatalf("current body presentation %q: %+v", name, err)
				}
			}
			assertBody(before, tc.op.Name())
			current := before
			for _, name := range []string{"renamed_once", "renamed_twice"} {
				old := current.MustProcedure(captured.Address())
				next := *old
				next.ObjectName = name
				next.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
				next.ObjectRevision++
				state := catalog.CloneState(current.State())
				state.UnbindName(catalog.DescriptorNameKey(old))
				state.SetDescriptor(next.Address(), &next)
				state.BindName(catalog.DescriptorNameKey(&next), next.Address())
				current = current.WithState(state)
				assertBody(current, name)
				if node.Name() != tc.op.Name() || node.Procedure != captured {
					t.Fatal("presentation changed immutable operation or capture")
				}
			}
			assertBody(before, tc.op.Name()) // Original snapshot and capture stay immutable.
			c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: current}}
			for _, reference := range []expr.RoutineReference{nil, (*catalog.Procedure)(nil), foreign.CatalogRead().MustProcedure(foreignExt.Members[tc.slot])} {
				bad := kernel
				bad.RoutineReference = reference
				_, err := bad.Eval(c)
				var diagnostic *sqlstate.Error
				if err == nil || errors.As(err, &diagnostic) {
					t.Fatalf("missing/foreign carrier acquired body presentation: %v", err)
				}
			}
			stale := *current.MustProcedure(captured.Address())
			stale.Strict = !stale.Strict
			state := catalog.CloneState(current.State())
			state.SetDescriptor(stale.Address(), &stale)
			_, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: current.WithState(state)}})
			var diagnostic *sqlstate.Error
			if err == nil || errors.As(err, &diagnostic) {
				t.Fatalf("stale declaration acquired body presentation: %v", err)
			}
			origin := &sqlstate.Error{Code: "22003", Message: "argument failure", Context: "argument origin"}
			argument := kernel
			argument.RoutineReference = nil
			argument.Args = append([]expr.Expr(nil), kernel.Args...)
			argument.Args[0] = earthDiagnosticArgumentExpr{Info: expr.Info{Usage: floating.Usage()}, failure: origin}
			if _, err := argument.Eval(c); err != origin {
				t.Fatalf("argument error acquired body context: %v", err)
			}
		})
	}
}
