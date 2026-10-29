package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestDeparseRoutineSignatures(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ }
	oid, err := sqltype.OIDValue(types[sqltype.Oid], 1397)
	if err != nil { t.Fatal(err) }
	for _, tc := range []struct{ signature DeparseSignature; name string }{
		{DeparseFunction, "pg_get_functiondef"},
		{DeparseFunctionArguments, "pg_get_function_arguments"},
		{DeparseFunctionIdentityArguments, "pg_get_function_identity_arguments"},
		{DeparseFunctionResult, "pg_get_function_result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []expr.Expr{transformUnitExpr{Info: expr.Info{Usage: types[sqltype.Oid].Usage()}, value: oid}}
			n := Deparse{Info: expr.Info{Usage: types[sqltype.Text].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Signature: tc.signature, Args: args}
			if err := n.Validate(); err != nil { t.Fatal(err) }
			if tc.signature.Name() != tc.name || tc.signature.PrettyIndex() != -1 || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren { t.Fatal("signature protocol", n) }
			if nullable, known := n.ResultNullability(expr.ResultSelect); !nullable || !known { t.Fatal("nullable TEXT") }
			if demanded, _, _ := n.SnapshotDemand(func(expr.Expr) bool { return false }); !demanded { t.Fatal("missing inquiry capture") }
			if demanded, _, _ := n.SnapshotDemand(func(expr.Expr) bool { return true }); demanded { t.Fatal("strict NULL capture") }
			render, err := n.Render()
			if err != nil || render.Name != tc.name || !render.Replay.CatalogSnapshot { t.Fatal(render, err) }
			rebuilt, err := n.Rebuild(n.Info, args)
			if err != nil || !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a,b) }, false) { t.Fatal(rebuilt,err) }
			bad := n
			bad.Args = nil
			if bad.Validate() == nil { t.Fatal("wrong arity accepted") }
			bad = n; bad.Property.Volatility = expr.Immutable
			if bad.Validate() == nil { t.Fatal("wrong volatility accepted") }
			bad = n; bad.Usage = types[sqltype.Int4].Usage()
			if bad.Validate() == nil { t.Fatal("wrong result accepted") }
			bad = n; bad.Args = []expr.Expr{transformUnitExpr{Info:expr.Info{Usage:types[sqltype.Text].Usage()}, value:sqltype.String(types[sqltype.Text],"1397")}}
			if bad.Validate() == nil { t.Fatal("wrong input accepted") }
		})
	}
}
