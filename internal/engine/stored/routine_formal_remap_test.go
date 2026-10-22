package stored

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestRoutineFormalRemapBoundary(t *testing.T) {
	var scalar, array *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		switch descriptor.ID() {
		case sqltype.Int4:
			scalar = descriptor
		case 1007:
			array = descriptor
		}
	}
	if scalar == nil || array == nil {
		t.Fatal("missing integer test descriptors")
	}
	formal := plan.ParameterExpr{Info: expr.Info{Usage: scalar.Usage()}, Number: 1}
	nested := plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: []expr.Expr{formal}}
	for _, node := range []expr.Expr{formal, nested} {
		if _, err := RemapExpression(node, nil, nil); err == nil {
			t.Fatal("generic expression admitted runtime parameter")
		}
		if _, err := RemapScalar(node, nil, nil, nil, nil); err == nil {
			t.Fatal("generic scalar admitted runtime parameter")
		}
		if _, err := remapScalar(node, nil, nil, nil, nil, 1, nil); err != nil {
			t.Fatal("closed routine formal rejected", err)
		}
	}
	mapped, err := remapScalar(nested, nil, nil, nil, nil, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	retained := mapped.(plan.ArrayExpr).Elements[0].(plan.ParameterExpr)
	if retained.Number != formal.Number || retained.ResultUsage() != formal.ResultUsage() {
		t.Fatal("formal identity/usage changed", retained)
	}
	for _, number := range []int{0, 2} {
		invalid := formal
		invalid.Number = number
		if _, err := remapScalar(invalid, nil, nil, nil, nil, 1, nil); err == nil {
			t.Fatal("out-of-frame formal admitted", number)
		}
	}
	if _, err := remapScalar(formal, nil, nil, nil, nil, 0, nil); err == nil {
		t.Fatal("empty formal namespace admitted parameter")
	}
	untyped := formal
	untyped.Usage = sqltype.Usage{}
	if _, err := remapScalar(untyped, nil, nil, nil, nil, 1, nil); err == nil {
		t.Fatal("untyped formal admitted")
	}
}
