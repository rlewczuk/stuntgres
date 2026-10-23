package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The procedure result is the operation's result, not the containing node's:
// NULLIF returns its cube operand while its selected equality returns BOOL.
type cubeComparisonCapture struct {
	ref         ids.ObjectRef
	left, right *sqltype.Descriptor
	name        string
	result      sqltype.ID
}

func cubeComparisonCaptureOf(node expr.Expr) (cubeComparisonCapture, bool) {
	capture := cubeComparisonCapture{result: sqltype.Bool}
	operator := "="
	direct := false
	switch n := node.(type) {
	case plan.CubeComparisonExpr:
		direct = true
		capture.ref = n.Ref
		if n.Args[0] != nil && n.Args[1] != nil {
			capture.left, capture.right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
		}
		if int(n.Kind) < len(extension.CubeComparisonNames) {
			capture.name = extension.CubeComparisonNames[n.Kind]
		}
		if n.Kind == 6 {
			capture.result = sqltype.Int4
		}
	case plan.ComparisonExpr:
		capture.ref, operator = n.RoutineRef, n.Operator
		capture.left, capture.right = n.Left.ResultUsage().Type, n.Right.ResultUsage().Type
	case plan.DistinctExpr:
		capture.ref = n.RoutineRef
		capture.left, capture.right = n.Left.ResultUsage().Type, n.Right.ResultUsage().Type
	case plan.ScalarArrayComparisonExpr:
		capture.ref, operator = n.RoutineRef, n.Operator
		capture.left, capture.right = n.Left.ResultUsage().Type, n.ElementType()
	case plan.ConditionalExpr:
		capture.ref = n.RoutineRef
		if n.Kind == parser.NullIf && len(n.Args) == 2 {
			capture.left, capture.right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
		}
	default:
		return capture, false
	}
	if !direct {
		switch operator {
		case "=":
			capture.name = "cube_eq"
		case "<>", "!=":
			capture.name = "cube_ne"
		case "<":
			capture.name = "cube_lt"
		case "<=":
			capture.name = "cube_le"
		case ">":
			capture.name = "cube_gt"
		case ">=":
			capture.name = "cube_ge"
		}
	}
	return capture, direct || capture.ref != (ids.ObjectRef{}) || capture.left.IsCube() || capture.right.IsCube()
}

// The reference is already mapped. Never look up a source address or recover a
// selected routine from its spelling. Stock registration and the exact member
// connect the operation to its real declaration and both destination operands.
func remapCubeComparisonRoutine(view catalog.Read, reader catalog.DefinitionReader, captured *catalog.Procedure, capture cubeComparisonCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) (*catalog.Procedure, error) {
	owner := view.Owner()
	if owner == nil || capture.ref.Domain() != owner.Domain || capture.ref.Epoch != view.Epoch() || capture.ref.Address.ClassID != ids.ProcedureClassID || capture.ref.Address.SubID != 0 {
		return nil, fmt.Errorf("stuntgres: invalid captured cube comparison declaration (reference %v, epoch %v/current %v, registered owner %t)", capture.ref.Address, capture.ref.Epoch, view.Epoch(), owner != nil)
	}
	if !capture.left.IsCube() || capture.left != capture.right {
		return nil, fmt.Errorf("stuntgres: invalid captured cube comparison declaration (operand descriptors %v/%v)", capture.left, capture.right)
	}
	if captured == nil {
		return nil, fmt.Errorf("stuntgres: invalid captured cube comparison declaration (missing procedure)")
	}
	if captured.Implementation != extension.StockCube || owner.Extensions["cube"] != captured.Implementation {
		return nil, fmt.Errorf("stuntgres: invalid captured cube comparison declaration (stock registration for %s)", captured.Name())
	}
	if captured.Name() != capture.name || captured.Signature != extension.CubeComparisonSignature(capture.left.ID()) || captured.Result != ids.OID(capture.result) || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: invalid captured cube comparison declaration (selected %s(%s) result %v; expected %s(%s) result %v; kind %v strict %t volatility %v builtin %t)", captured.Name(), captured.Signature, captured.Result, capture.name, extension.CubeComparisonSignature(capture.left.ID()), capture.result, captured.RoutineKind, captured.Strict, captured.Volatility, captured.Builtin != nil)
	}
	left, right := typ(capture.left), typ(capture.right)
	if !left.IsCube() || left != right {
		return nil, fmt.Errorf("stuntgres: invalid mapped cube comparison operands")
	}
	base, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(left.ID())})
	if !exists || base.SQLType != left {
		return nil, fmt.Errorf("stuntgres: foreign mapped cube comparison type")
	}
	descriptor, _ := catalog.DescriptorAt(view, reader, capture.ref.Address)
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || current.Address() != capture.ref.Address || current.Name() != capture.name || current.Signature != extension.CubeComparisonSignature(left.ID()) || current.Result != ids.OID(capture.result) || current.RoutineKind != captured.RoutineKind || !current.Strict || current.Volatility != expr.Immutable || current.Implementation != captured.Implementation || current.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: missing remapped cube comparison declaration")
	}
	installation, exists := view.Extension(current.Extension)
	if !exists || len(installation.Members) != extension.CubeMemberCount {
		return nil, fmt.Errorf("stuntgres: missing mapped cube comparison installation")
	}
	member := -1
	for i, name := range extension.CubeComparisonNames {
		if name == capture.name {
			member = 3 + i
			break
		}
	}
	if member < 0 || installation.Members[member] != current.Address() || base.Extension != installation.Address() {
		return nil, fmt.Errorf("stuntgres: invalid mapped cube comparison member")
	}
	if err := owner.Extensions["cube"].ValidateExtension(view, installation); err != nil {
		return nil, err
	}
	return current, nil
}
