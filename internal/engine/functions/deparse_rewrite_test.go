package functions

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestDeparseRewriteSignatures(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	oid, err := sqltype.OIDValue(types[sqltype.Oid], 42)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		signature DeparseSignature
		name      string
		pretty    int
	}{{DeparseTrigger, "pg_get_triggerdef", -1}, {DeparseTriggerPretty, "pg_get_triggerdef", 1}, {DeparseRule, "pg_get_ruledef", -1}, {DeparseRulePretty, "pg_get_ruledef", 1}} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.pretty), func(t *testing.T) {
			args := []expr.Expr{transformUnitExpr{Info: expr.Info{Usage: types[sqltype.Oid].Usage()}, value: oid}}
			if tc.pretty >= 0 {
				args = append(args, transformUnitExpr{Info: expr.Info{Usage: types[sqltype.Bool].Usage()}, value: sqltype.Boolean(types[sqltype.Bool], true)})
			}
			node := Deparse{Info: expr.Info{Usage: types[sqltype.Text].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Signature: tc.signature, Args: args}
			if err := node.Validate(); err != nil {
				t.Fatal(err)
			}
			if tc.signature.Name() != tc.name || tc.signature.PrettyIndex() != tc.pretty || node.PlanningFold() != expr.FoldNever || node.PreflightFold() != expr.PreflightChildren || len(node.Children()) != len(args) {
				t.Fatal("signature protocol", node)
			}
			if catalog, _, _ := node.SnapshotDemand(func(expr.Expr) bool { return false }); !catalog {
				t.Fatal("missing statement snapshot demand")
			}
			if catalog, _, _ := node.SnapshotDemand(func(expr.Expr) bool { return true }); catalog {
				t.Fatal("strict NULL should not demand a catalog")
			}
			rendering, err := node.Render()
			if err != nil || rendering.Name != tc.name || len(rendering.Args) != len(args) || !rendering.Replay.CatalogSnapshot {
				t.Fatal("render protocol", rendering, err)
			}
			rebuilt, err := node.Rebuild(node.Info, append([]expr.Expr(nil), args...))
			if err != nil || rebuilt.(Deparse).Validate() != nil || !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
				t.Fatal("rebuild protocol", rebuilt, err)
			}
			if _, err := node.Rebuild(node.Info, args[:len(args)-1]); err == nil {
				t.Fatal("accepted wrong child count")
			}
			bad := node
			bad.Args = args[:len(args)-1]
			if err := bad.Validate(); err == nil {
				t.Fatal("accepted wrong signature arity")
			}
			bad = node
			bad.Property.Volatility = expr.Immutable
			if err := bad.Validate(); err == nil {
				t.Fatal("accepted non-STABLE node")
			}
		})
	}
}
