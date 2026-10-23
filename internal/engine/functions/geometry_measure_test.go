package functions

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestGeometryMeasureGroupIdentity(t *testing.T) {
	types := inspectCarrierTypes()
	// Typed NULL inputs make every numeric result NULL, but do not make the
	// selected overload interchangeable in a grouped/stored expression.
	makeNode := func(op sqltype.GeometryMeasure, namespace string) GeometryMeasure {
		t.Helper()
		d := types[op.Formal()]
		arg := stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: sqltype.Null(d)}
		n, err := NewGeometryMeasure(expr.Info{Usage: types[op.ResultID()].Usage()}, op, arg, namespace)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := makeNode(sqltype.AreaBox, "pg_catalog")
	sameChildren := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
	if !a.SameGroup(a, sameChildren, false) {
		t.Fatal("same measurement changed group")
	}
	for _, other := range []GeometryMeasure{
		makeNode(sqltype.AreaPath, "pg_catalog"),
		makeNode(sqltype.AreaCircle, "pg_catalog"),
		makeNode(sqltype.AreaBox, "other"),
	} {
		if a.SameGroup(other, sameChildren, false) {
			t.Fatal("different selected call merged")
		}
	}
	center := makeNode(sqltype.CenterBox, "pg_catalog")
	conversion, err := NewGeometryConversion(center.Info, sqltype.PointBox, [2]expr.Expr{center.Arg}, "pg_catalog")
	if err != nil {
		t.Fatal(err)
	}
	if center.SameGroup(conversion, sameChildren, false) {
		t.Fatal("center and point conversion merged")
	}
	rebuilt, err := a.Rebuild(a.Info, a.Children())
	if err != nil || !a.SameGroup(rebuilt, sameChildren, false) {
		t.Fatal("rebuild lost semantic group identity", err)
	}
}
