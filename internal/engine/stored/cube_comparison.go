package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
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
	return capture, direct || capture.left.IsCube() || capture.right.IsCube()
}

// The reference is already mapped. The scoped callback supplies the mapped
// ORIGINAL; current descriptors are used only to validate destination authority.
func remapCubeComparisonRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, capture cubeComparisonCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	owner := view.Owner()
	if owner == nil || capture.ref.Domain() != owner.Domain || capture.ref.Epoch != view.Epoch() || capture.ref.Address.ClassID != ids.ProcedureClassID || capture.ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid captured cube comparison reference")
	}
	if !capture.left.IsCube() || capture.left != capture.right {
		return fmt.Errorf("stuntgres: invalid captured cube comparison operands")
	}
	if captured == nil || original == nil || captured.Revision() == 0 || captured.Revision() != original.Revision() || captured.Implementation != extension.StockCube || captured.Signature != extension.CubeComparisonSignature(capture.left.ID()) || captured.Result != ids.OID(capture.result) || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Builtin != nil {
		return fmt.Errorf("stuntgres: invalid captured cube comparison declaration")
	}
	capture.left, capture.right = typ(capture.left), typ(capture.right)
	descriptor, _ := catalog.DescriptorAt(view, reader, capture.ref.Address)
	declaration, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(declaration, original) {
		return fmt.Errorf("stuntgres: missing remapped cube comparison declaration")
	}
	_, err := currentCubeComparisonProcedure(view, original, capture)
	return err
}

func currentCubeComparisonProcedure(view catalog.Read, original *catalog.Procedure, capture cubeComparisonCapture) (*catalog.Procedure, error) {
	if !capture.left.IsCube() || capture.left != capture.right {
		return nil, fmt.Errorf("stuntgres: invalid mapped cube comparison operands")
	}
	current, symbol, err := currentInstalledProcedure(view, capture.ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockCube || symbol != capture.name || current.Signature != extension.CubeComparisonSignature(capture.left.ID()) || current.Result != ids.OID(capture.result) || current.RoutineKind != catalog.FunctionRoutine || !current.Strict || current.Volatility != expr.Immutable || current.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: invalid mapped cube comparison declaration")
	}
	installation, exists := catalog.OriginInstallation(current)
	if !exists || len(installation.Members) != extension.CubeMemberCount {
		return nil, fmt.Errorf("stuntgres: missing mapped cube comparison installation")
	}
	base, exists := view.Type(installation.Members[0])
	if !exists || base.SQLType != capture.left || !catalog.SameInstallation(base, installation) {
		return nil, fmt.Errorf("stuntgres: foreign mapped cube comparison type")
	}
	member := -1
	for i, name := range extension.CubeComparisonNames {
		if name == capture.name {
			member = 3 + i
			break
		}
	}
	if member < 0 || installation.Members[member] != current.Address() {
		return nil, fmt.Errorf("stuntgres: invalid mapped cube comparison member")
	}
	if err := extension.StockCube.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func currentCubeComparison(n plan.CubeComparisonExpr, view catalog.Read) (*catalog.Procedure, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	original, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	capture, _ := cubeComparisonCaptureOf(n)
	result, ok := view.LookupType(capture.result)
	if !ok || n.ResultUsage().Type != result {
		return nil, fmt.Errorf("stuntgres: foreign cube comparison result")
	}
	return currentCubeComparisonProcedure(view, original, capture)
}

func cubeComparisonRendering(n plan.CubeComparisonExpr, name string) (expr.Rendering, error) {
	spec, err := n.Render()
	if err != nil {
		return expr.Rendering{}, err
	}
	if spec.Name != name {
		spec.Name = name
		spec.Replay.Pieces[0] = expr.TextPiece(name + "(")
	}
	return spec, nil
}
